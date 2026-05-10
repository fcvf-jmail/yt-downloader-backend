package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv" // Добавили для конвертации строк в числа
	"strings"
	"time"

	"github.com/fcvf-jmail/yt-downloader/internal/tasks"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

type WorkerProcessor struct {
	rdb *redis.Client
}

func (wp *WorkerProcessor) handleVideoDownload(ctx context.Context, t *asynq.Task) error {
	var payload tasks.VideoDownloadPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("ошибка распаковки payload: %v", err)
	}
	
	taskID, _ := asynq.GetTaskID(ctx)
	log.Printf("📥 Задача [%s] Начинаю скачивание: %s", taskID, payload.VideoURL)

	if err := os.MkdirAll("downloads", os.ModePerm); err != nil {
		return fmt.Errorf("не удалось создать директорию: %v", err)
	}

	formatSelection := "bestvideo+bestaudio/best"
	expectedFiles := 0 // Считаем, сколько файлов мы будем качать

	var formats []string
	if payload.VideoFormatId != "" { 
		formats = append(formats, payload.VideoFormatId)
		expectedFiles++
	}
	if payload.AudioFormatId != "" { 
		formats = append(formats, payload.AudioFormatId)
		expectedFiles++
	}
	if len(formats) > 0 { 
		formatSelection = strings.Join(formats, "+") 
	} else {
		// Если ничего не передали, по дефолту yt-dlp качает видео + аудио
		expectedFiles = 2
	}

	cmd := exec.Command("yt-dlp",
		"-f", formatSelection,
		"-o", "downloads/%(id)s-%(height)s.%(ext)s",
		"--cookies", "ytCookies.txt",
		"--js-runtimes", "node",
		"--remote-components", "ejs:github",
		"--newline",
		"--downloader", "aria2c",
		"--downloader-args", "aria2c:-x 16 -s 16 -k 1M",
		payload.VideoURL,
	)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil { return fmt.Errorf("ошибка получения stdout: %v", err) }
	
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil { return fmt.Errorf("ошибка старта yt-dlp: %v", err) }

	progressRegexAria2 := regexp.MustCompile(`\(([0-9\.]+)%\)`)
	progressRegexYtDlp := regexp.MustCompile(`\[download\]\s+([0-9\.]+)%`)
	destRegex := regexp.MustCompile(`(?:Destination:\s+|Merging formats into\s+")(downloads/[^"\s]+)`)
	
	var finalFilePath string

	// Переменные для умного расчета процентов
	var lastPercent float64 = 0
	var currentFileIndex = 1

	scanner := bufio.NewScanner(stdoutPipe)

	for scanner.Scan() {
		line := scanner.Text()
		
		log.Printf("[yt-dlp] %s", line)
		
		var parsedPercent float64 = -1

		// Ловим проценты от aria2c или стандартного загрузчика
		if match := progressRegexAria2.FindStringSubmatch(line); len(match) > 1 {
			parsedPercent, _ = strconv.ParseFloat(match[1], 64)
		} else if strings.Contains(line, "[download]") {
			if match := progressRegexYtDlp.FindStringSubmatch(line); len(match) > 1 {
				parsedPercent, _ = strconv.ParseFloat(match[1], 64)
			}
		}

		if parsedPercent >= 0 {
			// Если процент резко упал (например, со 100% до 5%), значит мы начали качать второй файл (аудио)
			if parsedPercent < lastPercent-30.0 {
				currentFileIndex++
			}
			lastPercent = parsedPercent

			// Защита от выхода за пределы
			idx := currentFileIndex
			if idx > expectedFiles {
				idx = expectedFiles
			}

			// Высчитываем общий процент:
			// Если expectedFiles = 2, то первый файл дает 0-50%, второй файл 50-100%
			overallProgress := (float64(idx-1)*100.0 + parsedPercent) / float64(expectedFiles)

			// Записываем в Redis общий процент (округляем до 1 знака)
			wp.rdb.Set(ctx, "progress:"+taskID, fmt.Sprintf("%.1f", overallProgress), time.Hour)
		}

		if match := destRegex.FindStringSubmatch(line); len(match) > 1 {
			finalFilePath = match[1] 
		}
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("ошибка при скачивании yt-dlp: %v: %w", err, asynq.SkipRetry)
	}

	apiBaseURL := os.Getenv("API_BASE_URL")
	if apiBaseURL == "" {
		apiBaseURL = "http://localhost:8080"
	}

	downloadURL := "URL неизвестен"
	if finalFilePath != "" {
		fileName := filepath.Base(finalFilePath) 
		ext := filepath.Ext(fileName)            
		nameWithoutExt := strings.TrimSuffix(fileName, ext) 
		
		parts := strings.Split(nameWithoutExt, "-")
		if len(parts) >= 2 {
			height := parts[len(parts)-1] 
			videoID := strings.Join(parts[:len(parts)-1], "-") 
			
			downloadURL = fmt.Sprintf("%s/api/file?id=%s&height=%s&ext=%s&title=video", 
				apiBaseURL, videoID, height, strings.TrimPrefix(ext, "."))
		}
	}

	log.Printf("✅ Видео скачано!\n🔗 Доступно: %s", downloadURL)
	return nil
}

func startGarbageCollector(dir string, maxAge time.Duration) {
	ticker := time.NewTicker(1 * time.Minute)

	for range ticker.C {
		files, err := os.ReadDir(dir)
		if err != nil { continue }

		for _, f := range files {
			if f.IsDir() { continue }
			info, err := f.Info()
			if err != nil { continue }

			if time.Since(info.ModTime()) > maxAge {
				filePath := filepath.Join(dir, f.Name())
				err := os.Remove(filePath)
				if err == nil {
					log.Printf("🧹 [GC] Уничтожен старый файл: %s", f.Name())
				} else {
					log.Printf("❌ [GC] Не удалось удалить %s: %v", f.Name(), err)
				}
			}
		}
	}
}

func main() {
	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	
	go startGarbageCollector("downloads", 5*time.Minute)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: 5, 
		},
	)

	processor := &WorkerProcessor{rdb: rdb}
	mux := asynq.NewServeMux()	
	mux.HandleFunc(tasks.TypeVideoDownload, processor.handleVideoDownload)

	log.Println("👷 Worker Service запущен. Жду задачи...")
	
	if err := srv.Run(mux); err != nil {
		log.Fatalf("Ошибка запуска worker: %v", err)
	}
}