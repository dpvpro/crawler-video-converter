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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// Расширения файлов для поиска (через запятую)
	// SOURCE_EXTENSIONS = ".MOV,.mov,.MP4,.mp4,.AVI,.avi"
	SOURCE_EXTENSIONS = ".MOV,.mov"
	// Выходное расширение
	OUTPUT_EXTENSION = ".mkv"
	// Имя каталога для конвертированных файлов
	CONVERTED_DIR = "converted"

	// === КОНТРОЛЬ ЗАГРУЗКИ CPU ===
	// Целевая суммарная загрузка CPU в процентах
	// 50 = использовать 50% от всех ядер
	// 100 = использовать все ядра
	TARGET_CPU_PERCENT = 50

	// Максимальное количество одновременных конвертаций
	// Рассчитывается динамически на основе TARGET_CPU_PERCENT
	MAX_WORKERS = 2

	// Nice level для процессов ffmpeg (0-19, больше = ниже приоритет)
	NICE_LEVEL = 19
)

// VideoFile представляет видео файл для конвертации
type VideoFile struct {
	sourcePath string
	sourceDir  string
	fileName   string
}

// ProcessManager управляет процессами конвертации
type ProcessManager struct {
	ctx       context.Context
	cancel    context.CancelFunc
	processes map[int]*exec.Cmd
	mu        sync.Mutex
	wg        *sync.WaitGroup
}

// NewProcessManager создает новый менеджер процессов
func NewProcessManager() *ProcessManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProcessManager{
		ctx:       ctx,
		cancel:    cancel,
		processes: make(map[int]*exec.Cmd),
		wg:        &sync.WaitGroup{},
	}
}

// RegisterProcess регистрирует новый процесс
func (pm *ProcessManager) RegisterProcess(cmd *exec.Cmd) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if cmd.Process != nil {
		pm.processes[cmd.Process.Pid] = cmd
	}
}

// UnregisterProcess удаляет процесс из реестра
func (pm *ProcessManager) UnregisterProcess(pid int) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	delete(pm.processes, pid)
}

// Shutdown корректно завершает все процессы
func (pm *ProcessManager) Shutdown() {
	fmt.Println("\n[ЗАВЕРШЕНИЕ] Получен сигнал прерывания, останавливаем конвертацию...")

	// Отменяем контекст
	pm.cancel()

	// Отправляем SIGTERM всем процессам
	pm.mu.Lock()
	for pid, cmd := range pm.processes {
		if cmd.Process != nil {
			fmt.Printf("  Отправка SIGTERM процессу PID=%d\n", pid)
			if runtime.GOOS != "windows" {
				cmd.Process.Signal(syscall.SIGTERM)
			} else {
				cmd.Process.Kill()
			}
		}
	}
	pm.mu.Unlock()

	// Даем процессам время на корректное завершение
	done := make(chan struct{})
	go func() {
		pm.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		fmt.Println("[ЗАВЕРШЕНИЕ] Все процессы завершены корректно")
	case <-time.After(10 * time.Second):
		fmt.Println("[ЗАВЕРШЕНИЕ] Принудительная остановка процессов...")
		pm.mu.Lock()
		for pid, cmd := range pm.processes {
			if cmd.Process != nil {
				fmt.Printf("  Принудительная остановка процесса PID=%d\n", pid)
				cmd.Process.Kill()
			}
		}
		pm.mu.Unlock()
		time.Sleep(1 * time.Second)
	}

	// Удаляем незавершенные файлы
	cleanupIncompleteFiles()
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
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Использование: %s <путь_к_каталогу>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nПрограмма рекурсивно обходит указанный каталог, находит видео файлы\n")
		fmt.Fprintf(os.Stderr, "с расширениями %s и конвертирует их в формат MKV с кодеком AV1.\n", SOURCE_EXTENSIONS)
		fmt.Fprintf(os.Stderr, "\nРезультаты сохраняются в подкаталог '%s' рядом с исходными файлами.\n", CONVERTED_DIR)
		fmt.Fprintf(os.Stderr, "\nОграничение нагрузки:\n")
		fmt.Fprintf(os.Stderr, "  - Целевая загрузка CPU: %d%%\n", TARGET_CPU_PERCENT)
		fmt.Fprintf(os.Stderr, "  - Максимум параллельных конвертаций: %d\n", MAX_WORKERS)
		fmt.Fprintf(os.Stderr, "  - Nice level: %d (низкий приоритет)\n", NICE_LEVEL)
		flag.PrintDefaults()
	}
	flag.Parse()

	// Проверка наличия обязательного аргумента
	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Ошибка: не указан путь к каталогу\n\n")
		flag.Usage()
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
		log.Fatalf("Ошибка: ffmpeg не найден или не доступен: %v\nУстановите ffmpeg: https://ffmpeg.org/download.html", err)
	}

	// Расчет параметров CPU
	numCPU := runtime.NumCPU()
	threadsPerWorker := calculateThreadsPerWorker(numCPU)

	fmt.Printf("Начинаем обработку каталога: %s\n", rootPath)
	fmt.Printf("Поиск файлов с расширениями: %s\n", SOURCE_EXTENSIONS)
	fmt.Printf("Количество CPU ядер: %d\n", numCPU)
	fmt.Printf("Целевая загрузка CPU: %d%% (≈%d ядер)\n", TARGET_CPU_PERCENT, numCPU*TARGET_CPU_PERCENT/100)
	fmt.Printf("Параллельных конвертаций: %d\n", MAX_WORKERS)
	fmt.Printf("Потоков на процесс ffmpeg: %d\n", threadsPerWorker)
	fmt.Printf("Nice level: %d\n\n", NICE_LEVEL)

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

	// Обработка файлов с ограничением параллельности
	err = processFiles(files, pm, threadsPerWorker)
	if err != nil {
		if err == context.Canceled {
			fmt.Println("\n[ОТМЕНА] Обработка прервана пользователем")
		} else {
			log.Printf("[ОШИБКА] Критическая ошибка при обработке: %v", err)
		}
	}
}

// calculateThreadsPerWorker рассчитывает количество потоков для каждого ffmpeg
func calculateThreadsPerWorker(numCPU int) int {
	// Общее количество потоков = (процент CPU / 100) * количество ядер
	totalThreads := (TARGET_CPU_PERCENT * numCPU) / 100
	if totalThreads < 1 {
		totalThreads = 1
	}

	// Потоков на воркер = общее / количество воркеров
	threadsPerWorker := totalThreads / MAX_WORKERS
	if threadsPerWorker < 1 {
		threadsPerWorker = 1
	}

	return threadsPerWorker
}

// checkFFmpeg проверяет доступность ffmpeg
func checkFFmpeg() error {
	cmd := exec.Command("ffmpeg", "-version")
	err := cmd.Run()
	return err
}

// findVideoFiles рекурсивно ищет видео файлы в указанном каталоге
func findVideoFiles(rootPath string) ([]VideoFile, error) {
	var files []VideoFile
	extensions := strings.Split(SOURCE_EXTENSIONS, ",")

	err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Printf("Предупреждение: ошибка доступа к %s: %v", path, err)
			return nil // Продолжаем обход
		}

		// Пропускаем каталоги
		if info.IsDir() {
			// Пропускаем каталоги с конвертированными файлами
			if info.Name() == CONVERTED_DIR {
				return filepath.SkipDir
			}
			return nil
		}

		// Проверяем расширение файла
		for _, ext := range extensions {
			if strings.HasSuffix(strings.ToLower(path), strings.ToLower(ext)) {
				dir := filepath.Dir(path)
				base := filepath.Base(path)
				files = append(files, VideoFile{
					sourcePath: path,
					sourceDir:  dir,
					fileName:   base,
				})
				break
			}
		}

		return nil
	})

	return files, err
}

// processFiles обрабатывает список файлов с ограничением параллельности
func processFiles(files []VideoFile, pm *ProcessManager, threadsPerWorker int) error {
	semaphore := make(chan struct{}, MAX_WORKERS)

	successCount := 0
	skipCount := 0
	errorCount := 0
	canceledCount := 0
	var mu sync.Mutex

	for _, file := range files {
		// Проверяем, не был ли процесс прерван
		select {
		case <-pm.ctx.Done():
			canceledCount = len(files) - successCount - skipCount - errorCount
			break
		default:
		}

		pm.wg.Add(1)
		semaphore <- struct{}{} // Захватываем слот

		go func(f VideoFile) {
			defer pm.wg.Done()
			defer func() { <-semaphore }() // Освобождаем слот

			// Восстановление после паники
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[ПАНИКА] При обработке %s: %v", f.fileName, r)
					mu.Lock()
					errorCount++
					mu.Unlock()
				}
			}()

			result := processFile(f, pm, threadsPerWorker)

			mu.Lock()
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
			mu.Unlock()
		}(file)
	}

	pm.wg.Wait()

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

// processFile обрабатывает один файл
// Возвращает: 0 - успех, 1 - пропущен, 2 - ошибка, 3 - отменен
func processFile(file VideoFile, pm *ProcessManager, threadsPerWorker int) int {
	// Проверяем контекст перед началом
	select {
	case <-pm.ctx.Done():
		return 3
	default:
	}

	// Создаем путь для выходного файла
	convertedDir := filepath.Join(file.sourceDir, CONVERTED_DIR)

	// Создаем каталог converted если его нет
	if err := os.MkdirAll(convertedDir, 0755); err != nil {
		log.Printf("Ошибка создания каталога %s: %v", convertedDir, err)
		return 2
	}

	// Формируем имя выходного файла
	nameWithoutExt := strings.TrimSuffix(file.fileName, filepath.Ext(file.fileName))
	outputFileName := nameWithoutExt + OUTPUT_EXTENSION
	outputPath := filepath.Join(convertedDir, outputFileName)

	// Проверяем, существует ли уже конвертированный файл
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("[ПРОПУЩЕН] %s (уже существует)\n", file.fileName)
		return 1
	}

	fmt.Printf("[НАЧАЛО] %s (threads=%d)\n", file.fileName, threadsPerWorker)

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
	if runtime.GOOS == "windows" {
		// На Windows nice не доступен
		cmd = exec.Command("ffmpeg",
			"-i", file.sourcePath,
			"-threads", strconv.Itoa(threadsPerWorker),
			"-c:v", "libsvtav1",
			"-crf", "35",
			"-preset", "8",
			"-svtav1-params", "lp="+strconv.Itoa(threadsPerWorker),
			"-c:a", "aac",
			"-b:a", "128k",
			outputPath,
		)
	} else {
		// На Unix-подобных системах используем nice
		cmd = exec.Command("nice",
			"-n", strconv.Itoa(NICE_LEVEL),
			"ffmpeg",
			"-i", file.sourcePath,
			"-threads", strconv.Itoa(threadsPerWorker),
			"-c:v", "libsvtav1",
			"-crf", "35",
			"-preset", "8",
			"-svtav1-params", "lp="+strconv.Itoa(threadsPerWorker),
			"-c:a", "aac",
			"-b:a", "128k",
			outputPath,
		)
	}

	// Перенаправляем вывод ffmpeg (можно включить для отладки)
	// cmd.Stdout = nil
	// cmd.Stderr = nil

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stdout

	// Запускаем команду
	err := cmd.Start()
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
		fmt.Printf("[ОТМЕНА] Остановка конвертации: %s\n", file.fileName)
		if cmd.Process != nil {
			cmd.Process.Kill()
			pm.UnregisterProcess(cmd.Process.Pid)
		}
		cleanup()
		return 3
	case err := <-done:
		// Процесс завершился
		if cmd.Process != nil {
			pm.UnregisterProcess(cmd.Process.Pid)
		}

		if err != nil {
			// Проверяем, была ли это отмена
			if pm.ctx.Err() != nil {
				cleanup()
				return 3
			}
			// Проверяем код выхода
			if exitErr, ok := err.(*exec.ExitError); ok {
				log.Printf("[ОШИБКА] ffmpeg завершился с кодом %d для файла %s", exitErr.ExitCode(), file.fileName)
			} else {
				log.Printf("[ОШИБКА] %s: %v", file.fileName, err)
			}
			cleanup()
			return 2
		}
	}

	// Проверяем, что файл создан и не пустой
	if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
		log.Printf("[ОШИБКА] %s: выходной файл пустой или не создан", file.fileName)
		cleanup()
		return 2
	}

	// Удаляем маркер неполного файла - конвертация успешна
	os.Remove(incompleteMarker)

	fmt.Printf("[ГОТОВО] %s -> %s\n", file.fileName, outputFileName)
	return 0
}

// cleanupIncompleteFiles удаляет неполные файлы конвертации
func cleanupIncompleteFiles() {
	incompleteCount := 0
	filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && strings.HasSuffix(path, ".incomplete") {
			// Удаляем маркер и соответствующий файл
			videoPath := strings.TrimSuffix(path, ".incomplete")
			if _, err := os.Stat(videoPath); err == nil {
				os.Remove(videoPath)
				incompleteCount++
			}
			os.Remove(path)
		}
		return nil
	})
	if incompleteCount > 0 {
		fmt.Printf("[ОЧИСТКА] Удалено неполных файлов: %d\n", incompleteCount)
	}
}
