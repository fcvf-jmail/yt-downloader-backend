# СТАДИЯ 1: Сборка и кодогенерация
FROM golang:1.25-alpine AS builder
WORKDIR /app

RUN apk add --no-cache protobuf
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

ENV PATH="$PATH:/go/bin"

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN protoc --go_out=. --go-grpc_out=. api/proto/metadata.proto
RUN CGO_ENABLED=0 go build -o /out/service-binary ./cmd/metadata-service

# ==========================================

# СТАДИЯ 2: Финальный образ с нужными утилитами
FROM alpine:latest
WORKDIR /app

# 1. Устанавливаем зависимости для yt-dlp (Python и FFmpeg)
RUN apk add --no-cache python3 py3-pip ffmpeg curl

# 2. Скачиваем актуальный yt-dlp и делаем его исполняемым
RUN curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp && \
    chmod a+rx /usr/local/bin/yt-dlp

# 3. Копируем наш собранный Go-бинарник из первой стадии
COPY --from=builder /out/service-binary .

CMD ["./service-binary"]