# ---- build stage ----
FROM golang:1.24 AS build
WORKDIR /src
# Cache deps
COPY go.mod go.sum ./
RUN go mod download
# Copy source and build static binary
COPY . .
# Change ./cmd according to your app location
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/app ./cmd

# ---- runtime stage (Alpine, có /bin/sh) ----
FROM alpine:3.20
# Nếu app gọi HTTPS, cần CA certs; thêm vài tool test cho tiện
RUN apk add --no-cache ca-certificates curl wget bash bind-tools iproute2 iputils busybox-extras tzdata \
 && update-ca-certificates

WORKDIR /app
COPY --from=build /out/app /app/app

# Chạy non-root cho an toàn
RUN adduser -D -u 65532 appuser && chown -R 65532:65532 /app
USER 65532:65532

EXPOSE 8080
ENTRYPOINT ["/app/app"]

    # # ---- runtime stage ----
# FROM gcr.io/distroless/static:nonroot
# WORKDIR /
# COPY --from=build /out/app /app
# USER 65532:65532          # nonroot
# EXPOSE 8080
# ENTRYPOINT ["/app"]