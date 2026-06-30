FROM node:22-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
RUN npm install
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS go-builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/gti-monitor ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache git openssh-client ca-certificates
WORKDIR /app
RUN mkdir -p /app /app/git /app/config /app/html /app/bin \
  && chmod 777 /app /app/git /app/config
COPY --from=go-builder /out/gti-monitor /app/bin/gti-monitor
COPY --from=web-builder /src/web/dist /app/html
EXPOSE 8080
ENV GTI_ADDR=:8080
ENV GTI_CONFIG_DIR=/app/config
ENV GTI_REPO_ROOT=/app/git
ENV GTI_HTML_DIR=/app/html
ENV GTI_COMMIT_USER_NAME=GitMonitor
ENV GTI_COMMIT_USER_EMAIL=gitmonitor@local
CMD ["/app/bin/gti-monitor"]
