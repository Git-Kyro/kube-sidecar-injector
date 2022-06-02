# Build the webhook binary
FROM golang:1.13 as builder

RUN apt-get -y update && apt-get -y install upx

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

# Copy the go source
COPY main.go main.go
COPY pkg pkg
COPY cmd cmd

# Build
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64
ENV GO111MODULE=on
ENV GOPROXY="https://goproxy.cn"

RUN go mod download 
RUN go build -a -o kube-sidecar-injector main.go 
RUN go build -a -o kube-sidecar-injector-tls cmd/tls/main.go 
RUN upx kube-sidecar-injector-tls kube-sidecar-injector

FROM alpine:3.9.2 as kube-sidecar-injector
RUN apk add tzdata
ENV TZ Asia/Shanghai
COPY --from=builder /workspace/kube-sidecar-injector .
ENTRYPOINT ["/kube-sidecar-injector"]

FROM alpine:3.9.2 as kube-sidecar-injector-tls
COPY --from=builder /workspace/kube-sidecar-injector-tls .
RUN apk add tzdata
ENV TZ Asia/Shanghai
ENTRYPOINT ["/kube-sidecar-injector-tls"]
# nerdctl build --target kube-sidecar-injector  -t registry.cn-shenzhen.aliyuncs.com/kyro/kube-sidecar-injector:latest .
# nerdctl build --target kube-sidecar-injector-tls  -t registry.cn-shenzhen.aliyuncs.com/kyro/kube-sidecar-injector-tls:latest .
