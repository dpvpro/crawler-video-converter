package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Константы программы
const (
	// Расширения исходных видео файлов
	// sourceExtensions = ".mov,.mp4,.avi"
	sourceExtensions = ".mov"
	// Выходное расширение
	outputExtension = ".mkv"
	// Имя каталога для конвертированных файлов
	convertedDir = "converted"
	// Nice level для процессов ffmpeg (0-19, больше = ниже приоритет)
	niceLevel = 10
	// Количество потоков ffmpeg по умолчанию
	defaultThreads = 2
)

// VideoFile представляет видео файл для обработки
type VideoFile struct {
	sourcePath string // Полный путь к исходному файлу
	sourceDir  string // Каталог исходного файла
	fileName   string // Имя файла с расширением
}

// ProcessManager управляет процессами и обеспечивает корректное завершение
type ProcessManager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	processes map[*exec.Cmd]bool
	mu        sync.Mutex
	wg        sync.WaitGroup
}

// NewProcessManager создает новый менеджер процессов
func NewProcessManager() *ProcessManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProcessManager{
		ctx:       ctx,
		cancel:    cancel,
		processes: make(map[*exec.Cmd]bool),
	}
}

// RegisterProcess регистрирует процесс для отслеживания
func (pm *ProcessManager) RegisterProcess(cmd *exec.Cmd) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.processes[cmd] = true
}

// UnregisterProcess удаляет процесс из отслеживания
func (pm *ProcessManager) UnregisterProcess(cmd *exec.Cmd) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.processes, cmd)
}

// Shutdown корректно завершает все процессы
func (pm *ProcessManager) Shutdown() {
	fmt.Println("\n[ЗАВЕРШЕНИЕ] Завершаем работу...")
	pm.cancel()

	// Сначала пытаемся корректно завершить процессы
	pm.mu.Lock()
	for cmd := range pm.processes {
		if cmd.Process != nil {
			fmt.Printf("[ЗАВЕРШЕНИЕ] Останавливаем процесс PID %d\n", cmd.Process.Pid)
			cmd.Process.Signal(os.Interrupt)
		}
	}
	pm.mu.Unlock()

	// Ждем завершения с таймаутом
	done := make(chan struct{})
	go func() {
		pm.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("[ЗАВЕРШЕНИЕ] Все процессы корректно завершены")
	case <-time.After(10 * time.Second):
		fmt.Println("[ЗАВЕРШЕНИЕ] Таймаут, принудительное завершение")
		pm.mu.Lock()
		for cmd := range pm.processes {
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		}
		pm.mu.Unlock()
	}
}

func main() {
	// Создаем менеджер процессов
	pm := NewProcessManager()

	// Обработка сигналов прерывания
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		sig := <-sigChan
		fmt.Printf("\n[СИГНАЛ] Получен сигнал: %v\n", sig)
		pm.Shutdown()
		os.Exit(130) // Стандартный код выхода при Ctrl+C
	}()

	// Парсинг аргументов командной строки
	var threads int
	flag.IntVar(&threads, "threads", defaultThreads, "Количество потоков для ffmpeg")
	flag.IntVar(&threads, "t", defaultThreads, "Количество потоков для ffmpeg (сокращенная форма)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Использование: %s [опции] <путь_к_каталогу>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nПрограмма рекурсивно обходит указанный каталог, находит видео файлы\n")
		fmt.Fprintf(os.Stderr, "с расширениями %s и конвертирует их в формат MKV с кодеком AV1.\n", sourceExtensions)
		fmt.Fprintf(os.Stderr, "\nРезультаты сохраняются в подкаталог '%s' рядом с исходными файлами.\n", convertedDir)
		fmt.Fprintf(os.Stderr, "\nОпции:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nОграничение нагрузки:\n")
		fmt.Fprintf(os.Stderr, "  - Потоков ffmpeg: %d (по умолчанию)\n", defaultThreads)
		fmt.Fprintf(os.Stderr, "  - Nice level: %d (низкий приоритет)\n", niceLevel)
	}
	flag.Parse()

	// Проверка наличия обязательного аргумента
	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Ошибка: не указан путь к каталогу\n\n")
		flag.Usage()
		os.Exit(1)
	}

	// Проверка корректности количества потоков
	if threads < 1 {
		fmt.Fprintf(os.Stderr, "Ошибка: количество потоков должно быть положительным числом\n")
		os.Exit(1)
	}

	rootPath := flag.Arg(0)

	// Проверка существования каталога
	info, err := os.Stat(rootPath)
	if err != nil {
		log.Fatalf("Ошибка доступа к пути '%s': %v", rootPath, err)
	}
	if !info.IsDir() {
		log.Fatalf("Ошибка: '%s' не является каталогом", rootPath)
	}

	// Проверка наличия ffmpeg
	if err := checkFFmpeg(); err != nil {
		log.Fatalf("Ошибка: ffmpeg не найден или не доступен: %v\n", err)
	}

	fmt.Printf("Начинаем обработку каталога: %s\n", rootPath)
	fmt.Printf("Поиск файлов с расширениями: %s\n", sourceExtensions)
	fmt.Printf("Потоков ffmpeg: %d\n", threads)
	fmt.Printf("Nice level: %d\n\n", niceLevel)

	// Поиск видео файлов
	files, err := findVideoFiles(rootPath)
	if err != nil {
		log.Fatalf("Ошибка при поиске файлов: %v", err)
	}

	if len(files) == 0 {
		fmt.Println("Видео файлы не найдены")
		return
	}

	fmt.Printf("Найдено %d файлов для обработки\n\n", len(files))

	// Обработка файлов последовательно
	err = processFiles(files, pm, threads)
	if err != nil {
		if err == context.Canceled {
			fmt.Println("\n[ОТМЕНА] Обработка прервана пользователем")
		} else {
			log.Printf("[ОШИБКА] Критическая ошибка при обработке: %v", err)
		}
	}
}

// checkFFmpeg проверяет наличие ffmpeg в системе
func checkFFmpeg() error {
	_, err := exec.LookPath("ffmpeg")
	return err
}

// findVideoFiles рекурсивно ищет видео файлы в каталоге
func findVideoFiles(rootPath string) ([]VideoFile, error) {
	var files []VideoFile
	extensions := strings.Split(sourceExtensions, ",")

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		for _, allowedExt := range extensions {
			if ext == strings.ToLower(allowedExt) {
				files = append(files, VideoFile{
					sourcePath: path,
					sourceDir:  filepath.Dir(path),
					fileName:   filepath.Base(path),
				})
				break
			}
		}

		return nil
	})

	return files, err
}

// processFiles обрабатывает файлы последовательно
func processFiles(files []VideoFile, pm *ProcessManager, threads int) error {
	successCount := 0
	skipCount := 0
	errorCount := 0
	canceledCount := 0

	for _, file := range files {
		// Проверяем, не был ли процесс прерван
		if pm.ctx.Err() != nil {
			canceledCount = len(files) - successCount - skipCount - errorCount
			break
		}

		pm.wg.Add(1)
		result := processFile(file, pm, threads)
		pm.wg.Done()

		switch result {
		case 0:
			successCount++
		case 1:
			skipCount++
		case 2:
			errorCount++
		case 3:
			canceledCount++
		}
	}

	fmt.Printf("\n===== Результаты обработки =====\n")
	fmt.Printf("Успешно конвертировано: %d\n", successCount)
	fmt.Printf("Пропущено (уже существует): %d\n", skipCount)
	if canceledCount > 0 {
		fmt.Printf("Отменено: %d\n", canceledCount)
	}
	fmt.Printf("Ошибок: %d\n", errorCount)

	if pm.ctx.Err() != nil {
		return pm.ctx.Err()
	}
	return nil
}

// processFile обрабатывает один видео файл
func processFile(file VideoFile, pm *ProcessManager, threads int) int {
	// Проверяем контекст перед началом
	select {
	case <-pm.ctx.Done():
		return 3
	default:
	}

	// Получаем информацию об оригинальном файле для сохранения даты
	sourceInfo, err := os.Stat(file.sourcePath)
	if err != nil {
		log.Printf("[ОШИБКА] Не удалось получить информацию о файле %s: %v", file.fileName, err)
		return 2
	}

	// Создаем путь для выходного файла
	convertedDir := filepath.Join(file.sourceDir, convertedDir)

	// Создаем каталог converted если его нет
	if err := os.MkdirAll(convertedDir, 0755); err != nil {
		log.Printf("Ошибка создания каталога %s: %v", convertedDir, err)
		return 2
	}

	// Формируем имя выходного файла
	nameWithoutExt := strings.TrimSuffix(file.fileName, filepath.Ext(file.fileName))
	outputFileName := nameWithoutExt + outputExtension
	outputPath := filepath.Join(convertedDir, outputFileName)

	// Проверяем, существует ли уже конвертированный файл
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("[ПРОПУЩЕН] %s (уже существует)\n", file.fileName)
		return 1
	}

	fmt.Printf("[НАЧАЛО] %s (threads=%d)\n", file.fileName, threads)

	// Маркер для неполного файла
	incompleteMarker := outputPath + ".incomplete"

	// Создаем маркер неполного файла
	if err := os.WriteFile(incompleteMarker, []byte(time.Now().String()), 0644); err != nil {
		log.Printf("[ОШИБКА] Не удалось создать маркер для %s: %v", file.fileName, err)
		return 2
	}

	// Функция очистки при ошибке или отмене
	cleanup := func() {
		os.Remove(outputPath)
		os.Remove(incompleteMarker)
		fmt.Printf("[ОЧИСТКА] Удален неполный файл: %s\n", outputFileName)
	}

	// Запускаем ffmpeg с ограничением потоков
	var cmd *exec.Cmd
	// На Unix-подобных системах используем nice
	cmd = exec.Command("nice",
		"-n", strconv.Itoa(niceLevel),
		"ffmpeg",
		"-i", file.sourcePath,
		"-threads", strconv.Itoa(threads),
		"-c:v", "libsvtav1",
		"-crf", "25",
		"-preset", "8",
		"-svtav1-params", "lp="+strconv.Itoa(threads),
		"-c:a", "aac",
		"-b:a", "128k",
		outputPath,
	)

	// Перенаправляем вывод ffmpeg
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout

	// Запускаем команду
	err = cmd.Start()
	if err != nil {
		log.Printf("[ОШИБКА] Не удалось запустить ffmpeg для %s: %v", file.fileName, err)
		cleanup()
		return 2
	}

	// Регистрируем процесс
	pm.RegisterProcess(cmd)

	// Ожидаем завершения процесса или отмены
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case <-pm.ctx.Done():
		// Контекст отменен, останавливаем процесс
		if cmd.Process != nil {
			cmd.Process.Signal(os.Interrupt)
		}
		// Ждем завершения с таймаутом
		select {
		case <-done:
			// Процесс завершился сам
		case <-time.After(5 * time.Second):
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		}
		cleanup()
		pm.UnregisterProcess(cmd)
		return 3

	case err := <-done:
		// Процесс завершился
		pm.UnregisterProcess(cmd)

		if err != nil {
			log.Printf("[ОШИБКА] Ошибка конвертации %s: %v", file.fileName, err)
			cleanup()
			return 2
		}

		// Удаляем маркер неполного файла
		if err := os.Remove(incompleteMarker); err != nil {
			log.Printf("[ПРЕДУПРЕЖДЕНИЕ] Не удалось удалить маркер для %s: %v", file.fileName, err)
		}

		// Сохраняем оригинальную дату файла
		if err := os.Chtimes(outputPath, sourceInfo.ModTime(), sourceInfo.ModTime()); err != nil {
			log.Printf("[ПРЕДУПРЕЖДЕНИЕ] Не удалось сохранить дату файла %s: %v", file.fileName, err)
		}

		fmt.Printf("[УСПЕХ] %s -> %s\n", file.fileName, outputFileName)
		return 0
	}
}

// cleanupIncompleteFiles удаляет маркеры неполных файлов при запуске
func cleanupIncompleteFiles(rootPath string) {
	filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() && strings.HasSuffix(path, ".incomplete") {
			os.Remove(path)
			fmt.Printf("[ОЧИСТКА] Удален маркер неполного файла: %s\n", filepath.Base(path))
		}

		return nil
	})
}
