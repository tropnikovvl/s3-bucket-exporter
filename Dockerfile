FROM golang:1.24.1 AS builder

WORKDIR /build

COPY . .

RUN CGO_ENABLED=0 go build -a

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /build/s3-bucket-exporter /

ENTRYPOINT ["/s3-bucket-exporter"]
