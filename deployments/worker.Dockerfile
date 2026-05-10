FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/worker-service ./cmd/worker-service

FROM alpine:latest
WORKDIR /app

# ДОБАВЛЕНО: пакет aria2
RUN apk add --no-cache python3 curl ffmpeg nodejs npm aria2

RUN curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp && \
    chmod a+rx /usr/local/bin/yt-dlp

COPY --from=builder /out/worker-service .
RUN chmod +x ./worker-service

CMD ["./worker-service"]