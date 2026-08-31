FROM golang:1.27-alpine AS builder

RUN apk add --no-cache tzdata ca-certificates
WORKDIR /app

# scratchには/tmpが存在しない。動画処理(ffmpeg)の一時ファイル置き場として、
# ここで作った空ディレクトリを最終ステージへそのままコピーする。
# 最終イメージは非 root (65532) で動くため、パーミッションは 1777（sticky 付き全書き込み可）に
# しておく。既定の 0755 のままだと os.CreateTemp が permission denied で失敗し、
# 動画の結合・変換が一切できなくなる。
RUN mkdir -p /tmp && chmod 1777 /tmp

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/main ./main.go

# 静的リンクされたffmpegバイナリ(シェル・共有ライブラリ依存なし)。
# scratchベースの最終イメージにこの1バイナリだけをコピーする。
FROM mwader/static-ffmpeg:9.0 AS ffmpeg

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /tmp /tmp
COPY --from=ffmpeg /ffmpeg /usr/local/bin/ffmpeg

WORKDIR /app

COPY --chown=65532:65532 --from=builder /app/main /app/main
ENV TZ=Asia/Tokyo
ENV PATH=/usr/local/bin
EXPOSE 8080

# 非 root で実行する。scratch にシェルもパッケージマネージャも無いとはいえ、
# 侵入されたときに root のままだとコンテナ内で出来ることが増える。
# ローカルへの書き込みは os.CreateTemp 経由の /tmp だけで、成果物は GCS へ送るため、
# 権限を落としても動作に必要なものは失われない。
USER 65532:65532

CMD ["/app/main"]