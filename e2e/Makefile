##@ General

.PHONY: create-env
create-env:  ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	sh scripts/install.sh ${USER}

# Integration Test

.PHONY: envtest  
envtest: #test       			
	export PATH=$PATH:/usr/local/go/bin 
	ginkgo

.PHONY: delete-env
delete-env:
	#export PATH=$PATH:/opt/local/bin
	/usr/local/bin/k3s-uninstall.sh 
	cd setup/rook/ && kubectl delete -k. 
