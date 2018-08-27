.PHONY: docker 

-include .rc.mk

stackdriveradapter: cmd/main.go
	cd cmd && ../../../vendor/istio.io/istio/bin/gobuild.sh stackdriveradapter .

docker: cmd/stackdriveradapter
	cd cmd && docker build . -t $(HUB)/stackdriveradapter:$(TAG)

docker.push:
	docker push $(HUB)/stackdriveradapter:$(TAG)