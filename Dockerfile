FROM golang:1.21-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/xray-subscription .

FROM alpine:3.20

COPY --from=builder /out/xray-subscription /usr/local/bin/xray-subscription

EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/xray-subscription"]
