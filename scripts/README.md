
# Volume-migration-script

Pre-requisites

Following packages need to be installed on env.
- kubectl
- onmetal-image(To fetch Snapshot Ref) (https://github.com/onmetal/onmetal-image)
- ceph(Monitor IPs details) client
- bc (Arithmatic operation)

# Run the script with deployment. (Default deployment is with Dry Run flag and `rook-ceph` namespace and need to set env mon end point, env kube config, admin ceph secret)
`kubectl apply -f ./scripts/migration.yaml`

# If need to run simple script without deployment then do following things.

## Location: 
`cd ./scripts`

## Dry run:
```
./volume-migration-script.sh -d dry_run -n <Namespace> >> volume-migration.log
e.g.
./volume-migration-script.sh -d dry_run -n rook-ceph >> volume-migration.log
```



## Update script:
```
./volume-migration-script.sh -n <Namespace> >> volume-migration.log
e.g.
./volume-migration-script.sh -n rook-ceph >> volume-migration.log
```
 