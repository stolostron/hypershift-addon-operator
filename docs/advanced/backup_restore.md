# Backup and Restore

Refer to the ACM documentation on backup and restore for more details. https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.7/html/backup_and_restore/backup-intro

This documentation provides instructions on how to configure ACM on a service cluster to backup ACM resources so that when the service cluster fails over to a new cluster, ACM can restore manageability of all management clusters and managed guest hypershift hosted clusters. In this service cluster backup and restore example, we are going to configure one service cluster to be the active ACM cluster that backs up necessary resources periodically to a AWS S3 bucket, configure another service cluster to be a passive ACM cluster that continuously restore the backed up resources and stay passive by not trying to connect and manage the management and hypershift hosted clusters. After this active-passive environment is setup, we are going to fail over to the passive service cluster and make it the active service cluster that starts to manage the management and hypershift hosted clusters as well as to continue backing up the resources.

This sample uses an AWS S3 bucket for backup storage but you can use other supported storages. See https://github.com/vmware-tanzu/velero/blob/main/site/content/docs/main/supported-providers.md for other supported backup storage providers.

## Configuring ACM on active service cluster

1. Enable the backup feature in ACM.

```
      $ oc edit mch multiclusterhub -n open-cluster-management

          - enabled: true
            name: cluster-backup
```

2. Enable managed cluster auto-import recovery feature in MCE.

```
      $ oc edit mce multiclusterengine

          - enabled: true
            name: managedserviceaccount-preview
```

3. Create an AWS S3 bucket to store ACM backups.

4. Create the backup cloud secret. This secret is used by the ACM backup to get access to the AWS S3 bucket. Replace `/Users/sample/.aws/credentials` with your actual AWS credential file.

```
      oc create secret generic cloud-credentials --namespace open-cluster-management-backup --from-file cloud=/Users/sample/.aws/credentials
```

5. Create the following `DataProtectionApplication` instance.

```yaml
      apiVersion: oadp.openshift.io/v1alpha1
      kind: DataProtectionApplication
      metadata:
        name: dpa-active
        namespace: open-cluster-management-backup
      spec:
        configuration:
          restic:
            enable: true
          velero:
            defaultPlugins:
              - openshift
              - aws
        snapshotLocations:
          - name: default
            velero:
              config:
                profile: default                    <-- the AWS profile to use from the AWS credential file
                region: us-west-1                   <-- the AWS region
              provider: aws
        backupLocations:
          - velero:
              config:
                profile: default                    <-- the AWS profile to use from the AWS credential file
                region: us-west-1                   <-- the AWS region
              credential:
                key: cloud                          <-- the AWS credential key from the secret in the previous step
                name: cloud-credentials             <-- the AWS secret name from the previous step
              objectStorage:
                bucket: my-acm-backup-restore       <-- S3 bucket name
                prefix: my-acm-backup               <-- S3 folder name to store the backup data
              default: true
              provider: aws
```

6. Create the following `BackupSchedule` resource to start backing up and store them in the S3 bucket every hour.

```yaml
      apiVersion: cluster.open-cluster-management.io/v1beta1
      kind: BackupSchedule
      metadata:
        name: schedule-acm
        namespace: open-cluster-management-backup
      spec:
        veleroSchedule: 0 */1 * * *         <-- every hour (cron scheduling)
        veleroTtl: 12h                      <-- backup data time to live
        useManagedServiceAccount: true      <-- this enables auto-recovery auto-reconnection of all management and hypershift clusters on service cluster fail-over
```

## Specifying resources to be backed up

See https://access.redhat.com/documentation/en-us/red_hat_advanced_cluster_management_for_kubernetes/2.7/html/backup_and_restore/backup-intro#resources-that-are-backed-up for the list of ACM resources that are automatically backed up. If you want ACM backup to back up resources other than the ones listed in this document, you need to add the special label to the resources. In this example, I want to backup:

- the manifestwork resources for creating HostedCluster and NodePool CRs on management clusters
- the manifestwork resource for configuring htpasswd of the hosted clusters
- any secrets or configmaps that are referenced by the HostedCluster and NodePool CRs

Add the following label to all these manifestwork resources. The existence of this label without any value indicates to the ACM backup that this resource needs to be part of the backup. This label can always be added to the resources that the cluster service component creates to provision a new hypershift cluster. If ACM backup is not enabled, this label will just be ignored.

```
    cluster.open-cluster-management.io/backup: ""
```


## Configuring ACM on passive service cluster

It is recommended that the passive cluster has identical set of operators and other configurations as the active cluster.

1. Enable the backup(restore) feature in ACM.

```
      $ oc edit mch multiclusterhub -n open-cluster-management

          - enabled: true
            name: cluster-backup
```

2. Enable managed cluster auto-import recovery feature in MCE.

```
      $ oc edit mce multiclusterengine

          - enabled: true
            name: managedserviceaccount-preview
```

3. Create the restore cloud secret. This secret is used by the ACM restore to get access to the AWS S3 bucket.

```
      $ oc create secret generic cloud-credentials --namespace open-cluster-management-backup --from-file cloud=/Users/rokej/.aws/credentials
```

4. Create the following `DataProtectionApplication` instance to connect to the same storage location where the active service cluster had backed up the data.

```yaml
      apiVersion: oadp.openshift.io/v1alpha1
      kind: DataProtectionApplication
      metadata:
        name: dpa-passive
        namespace: open-cluster-management-backup
      spec:
        configuration:
          restic:
            enable: true
          velero:
            defaultPlugins:
              - openshift
              - aws
        snapshotLocations:
          - name: default
            velero:
              config:
                profile: default                    <-- the AWS profile to use from the AWS credential file
                region: us-west-1                   <-- the AWS region
              provider: aws
        backupLocations:
          - velero:
              config:
                profile: default                    <-- the AWS profile to use from the AWS credential file
                region: us-west-1                   <-- the AWS region
              credential:
                key: cloud                          <-- the AWS credential key from the secret in the previous step
                name: cloud-credentials             <-- the AWS secret name from the previous step
              objectStorage:
                bucket: my-acm-backup-restore       <-- S3 bucket name
                prefix: my-acm-backup               <-- S3 folder name to store the backup data
              default: true
              provider: aws
```

5. Create the following Restore resource to start restoring the backed up resources from the active cluster. 

```yaml
      apiVersion: cluster.open-cluster-management.io/v1beta1
      kind: Restore
      metadata:
        name: restore-acm-passive-sync
        namespace: open-cluster-management-backup
      spec:
        syncRestoreWithNewBackups: true
        restoreSyncInterval: 10m                  <-- syncs every 10 minutes
        veleroManagedClustersBackupName: skip     <-- this needs to be skip for ACM to stay passive, not trying to manage management and hypershift clusters, we will set this differently when complete fail-over is required
        veleroCredentialsBackupName: latest       
        veleroResourcesBackupName: latest
        cleanupBeforeRestore: CleanupRestored
```

6. Verify that the backup resources are created on the passive service cluster.

7. Verify that there is no ACM managed clusters (management and hypershift clusters) by running the following command.  

```
      $ oc get managedcluster   
```

## Backup and restore hosted cluster

Before failing over to another service cluster to restore ACM, you also need to backup and store hosted clusters. Follow this [doc](https://docs.openshift.com/container-platform/4.15/hosted_control_planes/hcp-backup-restore-dr.html).


## Fail over to the passive service cluster

1. Completely shutdown the active service cluster.

2. On the passive service cluster, edit the Restore resource and set `veleroManagedClustersBackupName: latest`.

```yaml
      apiVersion: cluster.open-cluster-management.io/v1beta1
      kind: Restore
      metadata:
        name: restore-acm-passive-sync
        namespace: open-cluster-management-backup
      spec:
        syncRestoreWithNewBackups: true
        restoreSyncInterval: 10m                  <-- syncs every 10 minutes
        veleroManagedClustersBackupName: latest   <-- By setting this to latest, the ACM restore will restore all management and hypershift clusters
        veleroCredentialsBackupName: latest       
        veleroResourcesBackupName: latest
        cleanupBeforeRestore: CleanupRestored
```

3. After a few minutes, verify that all management and hypershift clusters are reconnected by running the following command and check the status of each managed cluster.

```
      $ oc get managedcluster
```

4. When everything is restored, create the the following BackupSchedule resource to start backing up and store them in the S3 bucket every hour.

```yaml
      apiVersion: cluster.open-cluster-management.io/v1beta1
      kind: BackupSchedule
      metadata:
        name: schedule-acm
        namespace: open-cluster-management-backup
      spec:
        veleroSchedule: 0 */1 * * *         <-- every hour (cron scheduling)
        veleroTtl: 12h                      <-- backup data time to live
        useManagedServiceAccount: true      <-- this enables auto-recovery auto-reconnection of all management and hypershift clusters on service cluster fail-over
```

