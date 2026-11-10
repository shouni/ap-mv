FROM golang:1.27-alpine AS builder

RUN apk add --no-cache tzdata ca-certificates
WORKDIR /app

# scratch has no /tmp. The empty directory made here is copied as-is into the
# final stage as the scratch space for video processing (ffmpeg) temp files.
# The final image runs as non-root (65532), so the permissions are 1777
# (world-writable with the sticky bit). Left at the default 0755,
# os.CreateTemp fails with permission denied and no video can be joined or converted.
RUN mkdir -p /tmp && chmod 1777 /tmp

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app/main ./main.go

# A statically linked ffmpeg binary (no shell or shared-library dependencies).
# Only this one binary is copied into the scratch-based final image.
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

# Run as non-root. Even though scratch has no shell or package manager, staying
# root would widen what an intruder could do inside the container.
# The only local writes are to /tmp via os.CreateTemp and artifacts go to GCS,
# so dropping privileges loses nothing the service needs.
USER 65532:65532

CMD ["/app/main"]