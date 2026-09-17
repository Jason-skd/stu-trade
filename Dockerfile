# syntax=docker/dockerfile:1.7

FROM golang:1.27.1-alpine AS develop
WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
COPY . .
CMD ["go", "run", "./cmd/stu-trade", "serve", "--profile=develop"]

FROM golang:1.27.1-alpine AS build
ARG VERSION=dev
ARG COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY migrations ./migrations
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/stu-trade ./cmd/stu-trade

FROM gcr.io/distroless/static-debian12:nonroot AS production
COPY --from=build /out/stu-trade /usr/local/bin/stu-trade
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/stu-trade"]
CMD ["serve", "--profile=production"]
