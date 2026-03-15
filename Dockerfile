FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./

# Download dependencies using host cache mount (BuildKit)
RUN --mount=type=cache,target=/root/go/pkg/mod \
    go mod download

COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o /out/xray-subscription .

FROM scratch

COPY --from=builder /out/xray-subscription /xray-subscription

EXPOSE 8080
ENTRYPOINT ["/xray-subscription"]
