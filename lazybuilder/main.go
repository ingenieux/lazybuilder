package main

import (
	"flag"
	"fmt"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/ingenieux/lazybuilder"
	"os"
)

func main() {
	host := "tcp://localhost:2375"

	var imageName string

	dir, _ := os.Getwd()

	flag.StringVar(&host, "host", host, "Docker Host")
	flag.StringVar(&dir, "dir", dir, "Base Directory")

	flag.Parse()

	if 2 != len(flag.Args()) {
		flag.Usage()
		//fmt.Printf("Usage: %s <image-name>\n", flag.Usage())
		os.Exit(1)
	} else {
		imageName = os.Args[1]
	}

	client, err := docker.NewClient(host)

	if nil != err {
		panic(err)
	}

	tarArchive, err := lazybuilder.BuildTar(dir)

	if nil != err {
		panic(err)
	}

	err = client.BuildImage(docker.BuildImageOptions{Name: imageName, InputStream: tarArchive, OutputStream: os.Stdout})

	if nil != err {
		panic(err)
	}

	fmt.Printf("Built image %s", imageName)

}
