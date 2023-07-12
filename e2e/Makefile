##@ General

.PHONY: create-env
create-env:  ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
          #export PATH=$PATH:/opt/local/bin
      	  curl -sfL https://get.k3s.io | sh -s - --docker
		  kubectl apply -k.


# Integration Test

.PHONY: envtest
envtest:  
	   export PATH=$PATH:/usr/local/go/bin 
	   ginkgo

.PHONY: delete-env
envtest:
	   export PATH=$PATH:/opt/local/bin
	   kubectl delete -k. 
	   ginkgo
