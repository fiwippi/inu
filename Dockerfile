# Build inu
FROM golang:1.21-alpine as builder

WORKDIR /app
COPY . .

RUN CGO_ENABLED=0 go build -o ./bin/inu cmd/*

# Run inu
FROM alpine:latest

COPY --from=builder /app/bin/inu /bin/inu

ENTRYPOINT ["/bin/inu"]
