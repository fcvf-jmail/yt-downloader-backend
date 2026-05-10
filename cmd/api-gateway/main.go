package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"net/url"
	"path/filepath"
	"strings"

	pb "github.com/fcvf-jmail/yt-downloader/api/proto"
	"github.com/fcvf-jmail/yt-downloader/internal/tasks"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type gateway struct {
	metadataClient pb.MetadataServiceClient
	asynqClient *asynq.Client
	asynqInspector *asynq.Inspector
	rdb *redis.Client // ДОБАВИТЬ ЭТО
}

func (g *gateway) handleGetInfo(w http.ResponseWriter, r *http.Request) {
	videoUrl := r.URL.Query().Get("url")
	
	if videoUrl == "" {
		http.Error(w, `{"error": "Параметр url обязателен"}`, http.StatusBadRequest)
		return
	}

	req := &pb.VideoRequest{ Url: videoUrl }
	resp, err := g.metadataClient.GetVideoInfo(r.Context(), req)

	if err != nil {
		log.Printf("Ошибка gRPC: %v", err)
		http.Error(w, `{"error": "Не удалось получить данные о видео"}`, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("Ошибка сериализация JSON: %v", err)
	}
}

type DownloadRequest struct {
	URL string `json:"url"`
	VideoFormatId string `json:"video_format_id,omitempty"`
	AudioFormatId string `json:"audio_format_id,omitempty"`
}

func (g *gateway) handleDownload(w http.ResponseWriter, r *http.Request) {
	var requestBody DownloadRequest

	if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
		http.Error(w, `{"error": "Неверный формат JSON"}`, http.StatusBadRequest)
		return
	}

	if requestBody.URL == "" {
		http.Error(w, `{"error": "Поле url обязательно"}`, http.StatusBadRequest)
		return
	}

	payload, err := json.Marshal(tasks.VideoDownloadPayload{
		VideoURL: requestBody.URL,
		VideoFormatId: requestBody.VideoFormatId,
		AudioFormatId: requestBody.AudioFormatId,
	})

	if err != nil {
		log.Printf("Ошибка при парсинге: %v", err)
		http.Error(w, `{"error": "Ошибка формирования задачи"}`, http.StatusInternalServerError)
		return
	}

	task := asynq.NewTask(tasks.TypeVideoDownload, payload)
	info, err := g.asynqClient.Enqueue(
		task, 
		asynq.Retention(1 * time.Hour),
		asynq.MaxRetry(0),
	)
	
	if err != nil {
		log.Printf("Ошибка добавления в очередь: %v", err)
		http.Error(w, `{"error": "Не удалось поставить в очередь"}`, http.StatusInternalServerError)
		return
	}

	log.Printf("Задача %s поставлена в очередь", info.ID)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "В очереди на скачивание",
		"task_id": info.ID,
	})
}

func (g *gateway) handleStatus(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "id")
	if taskID == "" {
		http.Error(w, `{"error": "Не указан ID задачи"}`, http.StatusBadRequest)
		return
	}

	taskInfo, err := g.asynqInspector.GetTaskInfo("default", taskID)
	if err != nil {
		http.Error(w, `{"error": "Задача не найдена"}`, http.StatusNotFound)
		return
	}

	// По умолчанию берем статус прямо из Asynq (active, pending, completed)
	status := taskInfo.State.String()
	progress := "0"
	errorMessage := "" // Сюда положим текст ошибки, если она есть
	
	if taskInfo.State == asynq.TaskStateActive {
		val, err := g.rdb.Get(r.Context(), "progress:"+taskID).Result()
		if err == nil {
			progress = val
		}
	} else if taskInfo.State == asynq.TaskStateCompleted {
		progress = "100"
	} else if taskInfo.State == asynq.TaskStateArchived && taskInfo.LastErr != "" {
		status = "failed"
		errorMessage = "Ошибка скачивания. Возможно, видео недоступно или заблокировано"
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(map[string]string{
		"task_id":  taskInfo.ID,
		"status":   status, 
		"progress": progress,
		"error":    errorMessage, // Фронтенд сможет показать это пользователю
	})
}

func (g *gateway) handleServeFile(w http.ResponseWriter, r *http.Request) {
	// 1. Получаем параметры из URL
	videoID := r.URL.Query().Get("id")
	height := r.URL.Query().Get("height")
	ext := r.URL.Query().Get("ext")
	title := r.URL.Query().Get("title")

	if videoID == "" || height == "" || ext == "" || title == "" {
		http.Error(w, "Не хватает параметров (id, height, ext, title)", http.StatusBadRequest)
		return
	}

	// 2. Склеиваем физическое имя файла, которое лежит на диске
	fileName := fmt.Sprintf("%s-%s.%s", videoID, height, ext)
	filePath := filepath.Join("downloads", fileName)

	// Проверяем, существует ли файл
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "Файл еще не скачан или удален", http.StatusNotFound)
		return
	}

	// 3. Формируем красивое имя файла для пользователя
	fullTitle := fmt.Sprintf("%s (%sp).%s", title, height, ext)
	encodedTitle := url.QueryEscape(fullTitle)
	encodedTitle = strings.ReplaceAll(encodedTitle, "+", "%20")

	// 4. Тот самый магический заголовок!
	// attachment - заставляет браузер скачать файл, а не открывать его как плеер.
	headerValue := fmt.Sprintf(`attachment; filename*=UTF-8''%s`, encodedTitle)
	w.Header().Set("Content-Disposition", headerValue)
	w.Header().Set("Content-Type", "video/mp4") // Или application/octet-stream

	// 5. Отдаем сам файл
	http.ServeFile(w, r, filePath)
}

func main() {
	metadataAddr := os.Getenv("METADATA_ADDR")
	
	if metadataAddr == "" {
		metadataAddr = "localhost:50051"
	}

	conn, err := grpc.NewClient(metadataAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Не удалось подключиться к Metadata Service: %v", err)
	}
	defer conn.Close()

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "localhost:6379"
	}

	// 1. Создаем клиента для записи задач
	asynqClient := asynq.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	defer asynqClient.Close()

	// 2. СОЗДАЕМ ИНСПЕКТОРА (Проверь, есть ли у тебя эти строки!)
	asynqInspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: redisAddr})
	defer asynqInspector.Close()

	// 3. Создаем обычный Redis-клиент для чтения процентов
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})

	// 4. КЛАДЕМ ВСЁ В СТРУКТУРУ GATEWAY (Проверь, что передал все 4 поля!)
	gw := &gateway{
		metadataClient: pb.NewMetadataServiceClient(conn),
		asynqClient:    asynqClient,
		asynqInspector: asynqInspector, // <-- ПАНИКА БЫЛА ИЗ-ЗА ТОГО, ЧТО ЭТОГО ПОЛЯ НЕ БЫЛО
		rdb:            rdb,
	}

	r := chi.NewRouter()

	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(cors.Handler(cors.Options{
		// Звездочка разрешает запросы с абсолютно любого домена
		AllowedOrigins:   []string{"*"}, 
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token"},
		ExposedHeaders:   []string{"Link", "Content-Disposition"}, 
		
		// ВАЖНОЕ ПРАВИЛО БРАУЗЕРОВ: 
		// Если ты разрешаешь доступ всем ("*"), браузер в целях безопасности 
		// запретит передавать куки и данные авторизации. 
		// Так как у нас нет логина/паролей для пользователей, ставим false.
		AllowCredentials: false, 
		
		MaxAge: 300,
	}))

	r.Get("/api/info", gw.handleGetInfo)
	r.Post("/api/download", gw.handleDownload)
	r.Get("/api/status/{id}", gw.handleStatus)
	r.Get("/api/file", gw.handleServeFile)

	// 4. Запускаем HTTP сервер
	log.Println("🚀 API Gateway запущен на http://localhost:8080")

	if err := http.ListenAndServe(":8080", r); err != nil {
		log.Fatalf("Ошибка HTTP сервера: %v", err)
	}
}