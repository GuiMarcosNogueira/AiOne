# syntax=docker/dockerfile:1
FROM golang:1.24 AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /app/bin/ai-aggregator ./cmd/server
RUN mkdir -p /app/storage

FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /app/bin/ai-aggregator ./ai-aggregator
COPY --from=build --chown=nonroot:nonroot /app/storage /app/storage
EXPOSE 8080
USER nonroot:nonroot
ENV HTTP_PORT=8080
CMD ["/app/ai-aggregator"]
