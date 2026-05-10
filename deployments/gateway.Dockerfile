# СТАДИЯ 1: Сборка и кодогенерация
FROM golang:1.25-alpine AS builder
WORKDIR /app

# 1. Устанавливаем protoc (компилятор protobuf)
RUN apk add --no-cache protobuf

# 2. Устанавливаем плагины Go для gRPC
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Добавляем плагины в PATH, чтобы protoc их нашел
ENV PATH="$PATH:/go/bin"

# 3. Копируем зависимости
COPY go.mod go.sum ./
RUN go mod download

# 4. Копируем весь исходный код (включая папку api/proto)
COPY . .

# 5. Генерируем Go-файлы из proto-контрактов
RUN protoc --go_out=. --go-grpc_out=. api/proto/metadata.proto

# 6. ВАЖНО: Собираем именно api-gateway!
RUN CGO_ENABLED=0 go build -o /out/service-binary ./cmd/api-gateway

# ==========================================

# СТАДИЯ 2: Финальный легковесный образ
FROM alpine:latest
WORKDIR /app

# Копируем готовый бинарник из первой стадии (builder)
COPY --from=builder /out/service-binary .

# Запускаем наш API Gateway
CMD ["./service-binary"]