##@ General

.PHONY: create-env
create-env:  ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	sh scripts/install.sh ${USER}

# Integration Test

.PHONY: envtest  
envtest: #test       			
	export PATH=$PATH:/usr/local/go/bin 
	ginkgo -- --ceph-pool=ceph --ceph-user=volume-rook-ceph--ceph --ceph-client=volume-rook-ceph--ceph --ceph-mornitors=[2a00:da8:ffef:1402::]:6789,[2a00:da8:ffef:1410::]:6789,[2a00:da8:ffef:1412::]:6789 --ceph-keyfile=/home/ppanditrao/cephlet/ori/volume/cmd/volume/keyring-only-ceph --volume-class=fast

.PHONY: deploy
deploy:	
	../bin/kustomize build ../config/cephlet-volume/default | kubectl apply -f - 

.PHONY: undeploy
undeploy:
	../bin/kustomize build ../config/cephlet-volume/default | kubectl delete -f -

.PHONY: delete-env
delete-env: 
	cd setup/rook/ && kubectl delete -k. 
	#sh scripts/uninstall.sh
