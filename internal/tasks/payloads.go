package tasks

// TypeVideoDownload — уникальное имя нашей задачи (чтобы воркер понимал, что делать)
const TypeVideoDownload = "task:video:download"

// VideoDownloadPayload — данные, которые мы кладем внутрь задачи
type VideoDownloadPayload struct {
	VideoURL string `json:"video_url"`
	VideoFormatId string `json:"video_format_id"`
	AudioFormatId string `json:"audio_format_id"`
}