# Hosted Control Plane Disaster Recovery

The control plane portion of a hosted cluster called hosted control plane runs on the MCE hub cluster while the data plane runs on a separate platform of your choice. So when recovering the MCE hub from a disaster, you also need to consider recovering the hosted control planes.

See [this](https://docs.openshift.com/container-platform/4.12/backup_and_restore/control_plane_backup_and_restore/disaster_recovery/dr-hosted-cluster-within-aws-region.html#dr-hosted-cluster-process) for how to backup a hosted cluster and restore it on another cluster. Note that this is currently only supported for AWS platform.