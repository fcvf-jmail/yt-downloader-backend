package ytdlp

import (
	"encoding/json"
	"os/exec"
)

type YTDLPInfo struct {
	ID string `json:"id"`
	Title string `json:"title"`
	Thumbnail string `json:"thumbnail"`
	Duration int `json:"duration"`
	Formats []YTDLPFormat `json:"formats"`
}

type YTDLPFormat struct {
	FormatId string `json:"format_id"`
	Width int `json:"width"`
	Height int `json:"height"`
	Ext string `json:"ext"`
	VCodec string `json:"vcodec"`
	ACodec string `json:"acodec"`
	Filesize int64 `json:"filesize"`        // Точный размер
    FilesizeApprox int64 `json:"filesize_approx"` // Примерный размер
    Abr float64 `json:"abr"`             // Audio bitrate
}

func ExtractVideoInfo(videoUrl string) (*YTDLPInfo, error) {
	cmd := exec.Command("yt-dlp", videoUrl, "--dump-json")
	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	var ytdlpInfo YTDLPInfo

	err = json.Unmarshal(out, &ytdlpInfo)
	
	if err != nil {
		return nil, err
	}

	return &ytdlpInfo, nil
}