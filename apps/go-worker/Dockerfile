FROM golang:1.24-alpine AS build
WORKDIR /src
# Build context is apps/go-worker/ (fly deploys from the fly.toml directory).
COPY go.mod go.sum* ./
RUN go mod download
COPY . ./
RUN CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/worker /worker
EXPOSE 8081
CMD ["/worker"]
