package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

const (
	// Расширения файлов для поиска (через запятую)
	SOURCE_EXTENSIONS = ".MOV,.mov,.MP4,.mp4,.AVI,.avi"
	// Выходное расширение
	OUTPUT_EXTENSION = ".mkv"
	// Имя каталога для конвертированных файлов
	CONVERTED_DIR = "converted"

	// === КОНТРОЛЬ ЗАГРУЗКИ CPU ===
	// Максимальное количество одновременных конвертаций
	MAX_WORKERS = 2

	// Целевая суммарная загрузка CPU в процентах
	// 50 = использовать 50% от всех ядер
	// 100 = использовать все ядра
	TARGET_CPU_PERCENT = 50
)

// VideoFile представляет видео файл для конвертации
type VideoFile struct {
	sourcePath string
	sourceDir  string
	fileName   string
}

func main() {
	// Парсинг аргументов командной строки
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Использование: %s <путь_к_каталогу>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nПрограмма рекурсивно обходит указанный каталог, находит видео файлы\n")
		fmt.Fprintf(os.Stderr, "с расширениями %s и конвертирует их в формат MKV с кодеком AV1.\n", SOURCE_EXTENSIONS)
		fmt.Fprintf(os.Stderr, "\nРезультаты сохраняются в подкаталог '%s' рядом с исходными файлами.\n", CONVERTED_DIR)
		fmt.Fprintf(os.Stderr, "\nОграничение нагрузки:\n")
		fmt.Fprintf(os.Stderr, "  - Максимум параллельных конвертаций: %d\n", MAX_WORKERS)
		fmt.Fprintf(os.Stderr, "  - Целевая загрузка CPU: %d%%\n", TARGET_CPU_PERCENT)
		fmt.Fprintf(os.Stderr, "  - Nice level: 10 (низкий приоритет)\n")
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
		log.Fatalf("Ошибка: ffmpeg не найден или не доступен: %v", err)
	}

	fmt.Printf("Начинаем обработку каталога: %s\n", rootPath)
	fmt.Printf("Поиск файлов с расширениями: %s\n", SOURCE_EXTENSIONS)
	fmt.Printf("Максимум параллельных конвертаций: %d\n", MAX_WORKERS)
	fmt.Printf("Количество CPU ядер: %d\n", runtime.NumCPU())
	fmt.Printf("Целевая загрузка CPU: %d%% (≈%d ядер)\n\n",
		TARGET_CPU_PERCENT,
		runtime.NumCPU()*TARGET_CPU_PERCENT/100)

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
	processFiles(files)
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
func processFiles(files []VideoFile) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, MAX_WORKERS)

	successCount := 0
	skipCount := 0
	errorCount := 0
	var mu sync.Mutex

	for _, file := range files {
		wg.Add(1)
		semaphore <- struct{}{} // Захватываем слот

		go func(f VideoFile) {
			defer wg.Done()
			defer func() { <-semaphore }() // Освобождаем слот

			result := processFile(f)

			mu.Lock()
			switch result {
			case 0:
				successCount++
			case 1:
				skipCount++
			case 2:
				errorCount++
			}
			mu.Unlock()
		}(file)
	}

	wg.Wait()

	fmt.Printf("\n===== Результаты обработки =====\n")
	fmt.Printf("Успешно конвертировано: %d\n", successCount)
	fmt.Printf("Пропущено (уже существует): %d\n", skipCount)
	fmt.Printf("Ошибок: %d\n", errorCount)
}

// processFile обрабатывает один файл
// Возвращает: 0 - успех, 1 - пропущен, 2 - ошибка
func processFile(file VideoFile) int {
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

	// Рассчитываем количество потоков для контроля загрузки
	numCPU := runtime.NumCPU()
	totalThreads := (TARGET_CPU_PERCENT * numCPU) / 100
	if totalThreads < 1 {
		totalThreads = 1
	}
	threadsPerWorker := totalThreads / MAX_WORKERS
	if threadsPerWorker < 1 {
		threadsPerWorker = 1
	}

	fmt.Printf("[НАЧАЛО] %s (threads=%d)\n", file.fileName, threadsPerWorker)

	// Создаем временный файл для конвертации
	tempPath := outputPath + ".tmp"

	// Запускаем ffmpeg с ограничением потоков
	cmd := exec.Command("nice",
		"-n", "10", // Понижаем приоритет
		"ffmpeg",
		"-i", file.sourcePath,
		"-threads", strconv.Itoa(threadsPerWorker), // Ограничиваем потоки
		"-c:v", "libsvtav1",
		"-crf", "35",
		"-preset", "8",
		"-svtav1-params", "lp="+strconv.Itoa(threadsPerWorker), // Потоки для AV1
		"-c:a", "aac",
		"-b:a", "128k",
		"-y", // Перезаписывать временный файл если существует
		tempPath,
	)

	// Перенаправляем вывод ffmpeg для отладки
	cmd.Stdout = os.Stdout // Можно установить os.Stdout для отладки
	cmd.Stderr = os.Stdout // Можно установить os.Stderr для отладки

	// Запускаем команду
	err := cmd.Run()

	if err != nil {
		// Удаляем временный файл при ошибке
		os.Remove(outputPath)
		log.Printf("[ОШИБКА] %s: %v", file.fileName, err)
		return 2
	}

	// Проверяем, что файл создан и не пустой
	if info, err := os.Stat(outputPath); err != nil || info.Size() == 0 {
		os.Remove(outputPath)
		log.Printf("[ОШИБКА] %s: выходной файл пустой или не создан", file.fileName)
		return 2
	}

	fmt.Printf("[ГОТОВО] %s -> %s\n", file.fileName, outputFileName)
	return 0
}

// copyFile копирует файл из src в dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		os.Remove(dst)
		return err
	}

	return destFile.Sync()
}
