# Configuring backup and restore for hosted control planes and hosted clusters

The OpenShift Hosted Control Plane Disaster Recovery documentation describes how to use the OpenShift API for Data Protection to backup and restore hosted clusters and explains how to install the OADP operator and create OADP resources. 

If you are using the Red Hat Advanced Cluster Management, you don’t need to install the OpenShift API for Data Protection operator in the default `openshift-adp` namespace, as per the hosted cluster documentation. Instead, enable the backup and restore component and use the OpenShift API for Data Protection operator installed by this component under the `open-cluster-management-backup` namespace.

In this case, the OADP resources such as `DataProtectionApplication`, `BackupSchedule`, `Backup` and `Restore` for your hosted clusters should be created in open-cluster-management-backup namespace.

When restoring the ACM hub cluster, it is recommended to restore the hosted clusters by following [OpenShift Hosted Control Plane Disaster Recovery documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/4.18/html/hosted_control_planes/high-availability-for-hosted-control-planes#hcp-disaster-recovery-oadp) before restoring the ACM hub.


Refer to [Overview of Hosted Cluster backup and restore process](https://docs.redhat.com/en/documentation/openshift_container_platform/4.18/html/hosted_control_planes/high-availability-for-hosted-control-planes#%20hcp-backup-restore-aws-overview_hcp-disaster-recovery-aws) and [OpenShift Hosted Control Plane Disaster Recovery documentation](https://docs.redhat.com/en/documentation/openshift_container_platform/4.18/html/hosted_control_planes/high-availability-for-hosted-control-planes#hcp-disaster-recovery-oadp) on how to backup and restore hosted clusters.