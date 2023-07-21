#Copyright 2022 OnMetal authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

##@ General

.PHONY: create-env
create-env:  ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	sh scripts/install.sh ${USER}

# Integration Test

.PHONY: envtest  
envtest: #test       			
	export PATH=$PATH:/usr/local/go/bin 
	ginkgo -- --ceph-pool=ceph --ceph-user=volume-rook-ceph--ceph --ceph-client=volume-rook-ceph--ceph --ceph-mornitors=[2a00:da8:ffef:1402::]:6789,[2a00:da8:ffef:1410::]:6789,[2a00:da8:ffef:1412::]:6789 --ceph-keyfile=/home/ppanditrao/cephlet/ori/volume/cmd/volume/keyring-only-ceph --volume-class=fast

.PHONY: delete-env
delete-env: 
	cd setup/rook/ && kubectl delete -k. 
	#sh scripts/uninstall.sh
