# ref: stackoverflow.com/a/57175575
FROM golang:1.18 AS builder

RUN       mkdir /app
WORKDIR   /app

ADD       . ./

RUN       go mod download
# cgo off: stackoverflow.com/a/70882080
RUN       CGO_ENABLED=0 go build -o ./gw

# github.com/GoogleContainerTools/distroless/blob/f4f2a30/examples/go/Dockerfile
FROM gcr.io/distroless/static AS runner

COPY --from=builder /app/gw /

CMD ["/gw"]
