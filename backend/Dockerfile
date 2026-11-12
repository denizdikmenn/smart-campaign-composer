# dev: hot reload with air; source is bind-mounted by docker-compose.dev.yml.
FROM golang:1.27-alpine AS dev
RUN go install github.com/air-verse/air@v1.67.4
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["air", "--build.cmd", "go build -o /tmp/api ./cmd/api", "--build.entrypoint", "/tmp/api", "--build.include_ext", "go"]

FROM dev AS build
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker

# Default target: production-like image (api by default; the worker service runs `worker`).
FROM alpine:3.23
RUN adduser -D -u 10001 app
USER app
COPY --from=build /out/api /out/worker /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["api"]
