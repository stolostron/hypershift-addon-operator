# Hypershift Hosted Control Plane Minimum Resource Requirements

A HyperShift hosted cluster's control plane runs on the hosting MCE/ACM cluster while its data plane runs on the cloud or infrastructure of your choice. Each hosted control plane has the following minimum resource requirements when `--control-plane-availability-policy` is set to `HighlyAvailable` which is recommended.

- The hosted control plane consists of 78 pods.
- The hosted control plane requires 3 PVs for Etcd and 3 PVs for OVN.
- Minimum CPU:  ~5.5cores
- Minimum memory: ~19GB

* These measurements are done with OCP 4.12.9

Based on this minimum resource requirements and the size of your hosting MCE/ACM cluster, you can determine the maximum number of HyperShift hosted clusters the hosting MCE/ACM cluster can host.