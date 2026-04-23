FROM golang:1.24-alpine AS build
WORKDIR /src
COPY apps/go-worker/go.mod apps/go-worker/go.sum* ./
RUN go mod download
COPY apps/go-worker ./
RUN CGO_ENABLED=0 go build -o /out/worker ./cmd/worker

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/worker /worker
EXPOSE 8081
CMD ["/worker"]
