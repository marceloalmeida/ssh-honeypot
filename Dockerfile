# Stage 1: Build the Go binary
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o ssh-honeypot .


FROM gcr.io/distroless/base-debian12

LABEL org.opencontainers.image.source https://github.com/marceloalmeida/ssh-honeypot

COPY --from=builder /app/ssh-honeypot /app/ssh-honeypot

WORKDIR /app

CMD ["/app/ssh-honeypot"]
