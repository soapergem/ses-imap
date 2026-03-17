FROM docker.io/library/golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /imap-server ./cmd/imap-server

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /imap-server /imap-server

USER nonroot:nonroot
EXPOSE 143

ENTRYPOINT ["/imap-server"]
