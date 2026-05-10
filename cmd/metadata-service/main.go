package main

import (
	"fmt"

	"context"
	"log"
	"net"

	pb "github.com/fcvf-jmail/yt-downloader/api/proto"
	"github.com/fcvf-jmail/yt-downloader/internal/ytdlp"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

type server struct {
	pb.UnimplementedMetadataServiceServer
}

func (s *server) GetVideoInfo (ctx context.Context, req *pb.VideoRequest) (*pb.VideoResponse, error) {
	log.Printf("Получен запрос на получение информации для URL: %s", req.GetUrl())
	ytdlpInfo, err := ytdlp.ExtractVideoInfo(req.GetUrl())

	if err != nil {
		log.Printf("Ошибка при парсинге видео: %v", err)
		return nil, status.Errorf(codes.Internal, "Не удалось получить информацию о видео: %v", err)
	}
	
	var pbFormats []*pb.Format

	for _, format := range ytdlpInfo.Formats {
		var size int64 = format.Filesize
		if size == 0 {
			size = format.FilesizeApprox // Если точного нет, берем примерный
		}
		pbFormats = append(pbFormats, &pb.Format{
			FormatId: format.FormatId,
            Resolution: fmt.Sprintf("%vx%v", format.Width, format.Height),
            Ext: format.Ext,
            HasAudio: format.ACodec != "none",
			FilesizeBytes: size,
			AudioBitrate: format.Abr,
		})
	}

	return &pb.VideoResponse{
		Title: ytdlpInfo.Title,
		ThumbnailUrl: ytdlpInfo.Thumbnail,
		Duration: fmt.Sprint(ytdlpInfo.Duration),
		Formats: pbFormats,
	}, nil
}

func main() {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		log.Fatalf("Не удалось открыть порт: %v", err)
	}

	grpcServer := grpc.NewServer()
	pb.RegisterMetadataServiceServer(grpcServer, &server{})

	log.Println("✅ Metadata Service запущен на порту 50051...")

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Ошибка при запуске gRPC сервера: %v", err)
	}
}


// func main() {
// 	ytdlpInfo := ytdlp.ExtractVideoInfo("https://youtu.be/Mw--mlN4i3c")
// 	fmt.Println(ytdlpInfo)
// }