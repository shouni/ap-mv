FROM golang:1.26-alpine AS builder

RUN apk add --no-cache tzdata ca-certificates
WORKDIR /app

# scratchには/tmpが存在しない。動画処理(ffmpeg)の一時ファイル置き場として、
# ここで作った空ディレクトリを最終ステージへそのままコピーする。
RUN mkdir -p /tmp

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/main ./main.go

# 静的リンクされたffmpegバイナリ(シェル・共有ライブラリ依存なし)。
# scratchベースの最終イメージにこの1バイナリだけをコピーする。
FROM mwader/static-ffmpeg:7.1 AS ffmpeg

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /tmp /tmp
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg

WORKDIR /app

COPY --from=builder /app/main /app/main
ENV TZ=Asia/Tokyo
ENV PATH=/usr/local/bin
EXPOSE 8080

CMD ["/app/main"]