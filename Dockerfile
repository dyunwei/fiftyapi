FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
COPY internal/i18n/en.json /src/internal/i18n/en.json
RUN npm run build

FROM golang:1.26.10-alpine AS build
ARG GOPROXY=https://goproxy.cn,direct
ARG BUILD_COMMIT=unknown
ENV GOPROXY=${GOPROXY}
RUN apk add --no-cache python3
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/internal/server/ui ./internal/server/ui
COPY --from=web /src/web/node_modules ./web/node_modules
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false -ldflags="-s -w -X fiftyapi/internal/buildinfo.Commit=${BUILD_COMMIT}" -o /out/fiftyapi ./cmd/fiftyapi
RUN python3 scripts/dependency-notices.py

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && addgroup -g 10001 app && adduser -D -u 10001 -G app app && mkdir -p /data /app && chown app:app /data /app
WORKDIR /app
COPY --from=build /out/fiftyapi /app/fiftyapi
COPY --from=build /src/dist/licenses /app/licenses
COPY LICENSE COPYING.GPL-3 THIRD_PARTY_NOTICES.md /app/
COPY --chmod=755 scripts/container-entrypoint.sh /app/entrypoint.sh
USER app
ENV FIFTYAPI_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD wget -q -T 3 -O - http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/app/entrypoint.sh"]
CMD ["serve"]
