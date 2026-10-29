FROM node:24-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26.10-alpine AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/create-admin ./cmd/create-admin

FROM backend AS host-storage-build
ARG HOST_ARCH=amd64
RUN CGO_ENABLED=0 GOOS=darwin GOARCH=${HOST_ARCH} go build -trimpath -o /out/host-storage ./cmd/host-storage

FROM scratch AS host-storage-export
COPY --from=host-storage-build /out/host-storage /host-storage

FROM alpine:3.23
RUN apk add --no-cache poppler-utils font-dejavu \
    && fc-cache -f \
    && addgroup -g 10001 vault && adduser -D -u 10001 -G vault vault \
    && mkdir /data && chown vault:vault /data && chmod 700 /data
WORKDIR /app
COPY --from=backend /out/server ./server
COPY --from=backend /out/create-admin ./create-admin
COPY --from=frontend /src/frontend/dist ./web
COPY LICENSE ./LICENSE
USER 10001:10001
ENV FAMILYVAULT_ADDR=0.0.0.0:8080 \
    FAMILYVAULT_CONTAINER=1 \
    FAMILYVAULT_DATA_DIR=/data \
    FAMILYVAULT_STATIC_DIR=/app/web
EXPOSE 8080
ENTRYPOINT ["/app/server"]
