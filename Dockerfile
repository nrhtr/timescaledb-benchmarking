FROM golang:1.16 as builder

WORKDIR /build

# Dependencies
ADD go.mod go.sum ./
RUN go mod download

# Build binary
ADD *.go /build
RUN CGO_ENABLED=0 GOOS=linux go build -a -o bench .

FROM alpine:3.15.0
COPY --from=builder /build/bench .
ADD entrypoint.sh .
CMD ["./bench"]  
