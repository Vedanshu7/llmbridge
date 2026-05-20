# Stage 1: build
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /llmbridge ./cmd/llmbridge

# Stage 2: minimal runtime image
FROM gcr.io/distroless/static-debian12
COPY --from=builder /llmbridge /llmbridge
EXPOSE 8080
ENTRYPOINT ["/llmbridge"]
