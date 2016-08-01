FROM golang
RUN apt-get update && apt-get -y install iptables dbus
RUN go get github.com/tools/godep
RUN curl -s -L https://get.docker.com/builds/Linux/x86_64/docker-1.6.0 > /usr/bin/docker; chmod +x /usr/bin/docker
RUN curl -s -L https://get.docker.com/builds/Linux/x86_64/docker-1.9.1 > /usr/bin/docker-1.9; chmod +x /usr/bin/docker-1.9
COPY . /go/src/github.com/chenleji/docker-bridge-plugin
WORKDIR /go/src/github.com/chenleji/docker-bridge-plugin
RUN godep go install -v
ENTRYPOINT ["docker-bridge-plugin"]
