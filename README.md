# lazybuilder - docker build on windows

Lazybuilder is a wrapper for Docker's build function. Basically, if you've got boot2docker, all you need is to call lazybuild instead of docker build

## Installation

If you've got a go environment, its as simple as:

```
C:\> go get github.com/ingenieux/lazybuilder/lazybuilder
```

Otherwise, fetch the binaries from [beta.gobuild.io](https://beta.gobuild.io/github.com/ingenieux/lazybuilder/lazybuilder)

## How to use it

Go into a directory containing a Dockerfile and type:

```
lazybuilder repo[:tag]
```

It will build, save and tag the image specified on the commandline, and output its logs.

## How it works?

docker build is in fact an API call, requiring only a single tar file containing the directory with the Dockerfile as its root. 

We've ported the relevant parts of the ```docker build``` command, in order not to rely on a system-installed tar package (as go already contains it), doing it internally as well as handling ```.dockerignore```.

By default, it tries to connect to ```localhost:2375```. If you're not on Windows, or want a different host, use the ```-host``` switch:

```
lazybuild -host=tcp:///<otherhost>:2375 myapp:latest
```

Or, if you're running it locally on a Linux host:

```
lazybuild -host=unix:///var/run/docker.dock myapp:latest
```

## Limitations

If you upload shell scripts or other linux binaries, **make sure your RUN directives set the permissions accordingly** after you ADD them, since Windows (the platform I've used to develop) is not aware of Unix permissions during the tar creation. 

(ok, at this point they must be, but its a lazy builder, you know?)