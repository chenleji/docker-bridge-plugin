docker-bridge-plugin
=================

### QuickStart Instructions

The quickstart instructions describe how to start the plugin in **nat mode**. 

**1.** Make sure you are using Docker 1.9 or later

**2.** Create the following `docker-compose.yml` file

```yaml
plugin:
  image: gopher-net/ovs-plugin
  volumes:
    - /run/docker/plugins:/run/docker/plugins
    - /var/run/docker.sock:/var/run/docker.sock
  net: host
  stdin_open: true
  tty: true
  privileged: true
  command: -d
```

**3.** `docker-compose up -d`

**4.** Now you are ready to create a new network

```
$ docker network create -d bridge mynet
```

**5.** Test it out!

```
$ docker run -itd --net=mynet --name=web nginx

$ docker run -it --rm --net=mynet busybox wget -qO- http://web
```

#### Trying it out

If you want to try out some of your changes with your local docker install

- `docker-compose -f dev.yml up -d`

This will start docker-bridge-plugin running inside a container!

### Thanks

Thanks to the guys at [Weave](http://weave.works) for writing their awesome [plugin](https://github.com/weaveworks/docker-plugin). We borrowed a lot of code from here to make this happen!
