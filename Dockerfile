FROM golang
RUN apt-get update && apt-get -y install iptables dbus
RUN go get github.com/tools/godep
COPY . /go/src/github.com/chenleji/docker-bridge-plugin
WORKDIR /go/src/github.com/chenleji/docker-bridge-plugin
RUN godep go install -v
ENTRYPOINT ["docker-bridge-plugin"]
