# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/t1menar/manufacture-core/internal/buildinfo.version=${VERSION}" -o /out/manufacture ./cmd/manufacture \
 && CGO_ENABLED=0 go build -trimpath -o /out/mfctl ./cmd/mfctl

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/manufacture /out/mfctl /usr/local/bin/
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/manufacture"]
CMD ["serve"]
