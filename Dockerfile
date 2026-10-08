FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/csi-demo ./cmd/csi-demo

FROM alpine:3.20
# e2fsprogs provides mkfs.ext4, util-linux provides losetup; both are needed
# once the loop device backend lands (Task 3).
RUN apk add --no-cache e2fsprogs util-linux
COPY --from=build /out/csi-demo /usr/local/bin/csi-demo
ENTRYPOINT ["/usr/local/bin/csi-demo"]