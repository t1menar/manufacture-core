# syntax=docker/dockerfile:1
FROM node:22-alpine AS web
WORKDIR /repo/web
COPY schemas /repo/schemas
COPY web/package.json web/package-lock.json ./
COPY web/scripts ./scripts
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /repo/web/dist ./web/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/t1menar/manufacture-core/internal/buildinfo.version=${VERSION}" -o /out/manufacture ./cmd/manufacture \
 && CGO_ENABLED=0 go build -trimpath -o /out/mfctl ./cmd/mfctl

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/manufacture /out/mfctl /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/manufacture"]
CMD ["serve"]
