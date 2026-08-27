FROM golang:1.26.6 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY main.go server.go ./
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -o god-morgen .

FROM gcr.io/distroless/static-debian12

WORKDIR /app
COPY --from=builder /app/god-morgen .

ENTRYPOINT ["/app/god-morgen"]
