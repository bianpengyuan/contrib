# Stackdriver Out of Process Adapter

This folder contains out of process Mixer Stackdriver adapter.

Steps to build and run:
```
# make sure all deps are ready
dep ensure

# build oop adapter
make stackdriveradapter

# Add .rc.mk file to specify hub and tag for stackdriver adapter
cat .rc.mk
HUB=your-hub
TAG=your-tag

# make adapter docker image
make docker

# push docker image
make docker.push

# apply yaml files to run adapter in your cluster. Example config includes exporting metrics, traces and logs.
export HUB="your-hub"
export TAG="your-tag"
export PROJECT_ID="your_project_id"
cat operatorconfig/sd-adapter-deployment.yaml | sed 's@{HUB}@'$HUB'@g' | sed 's@{TAG}@'$TAG'@g' | kubectl apply -f -
cat operatorconfig/stackdriver.yaml | sed 's/<project_id>/'$PROJECT_ID'/g' | kubectl apply -f -
kubectl apply -f operatorconfig/stackdriver-nosession.yaml
```