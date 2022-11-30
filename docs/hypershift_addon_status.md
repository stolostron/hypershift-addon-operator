# Hypershift Add-on Status

## Monitor the Hypershift Add-on Status on the Hub cluster

The Hypershift add-on is required on the management cluster to install the Hypershift operator. 
The namespace will be the managed cluster on which you want to install the HyperShift operator on.

An example of a healthy add-on status is:

```
$ oc -n local-cluster get managedclusteraddons hypershift-addon
NAME               AVAILABLE   DEGRADED   PROGRESSING
hypershift-addon   True        False
```

## Status Condition Type: Available 

The `Available` condition type is controlled by the add-on agent using ACM Addon Framework `Lease` health probe. 
The add-on agent maintains a `Lease` in its installation namespace with its status. 
The `Lease` is updated periodically by the add-on agent, and 
the registration agent will check this `Lease` to maintain the `Available` status of the ManagedClusterAddOn. 
When the add-on agent is healthy, the `Available` condition should have the value of `True`.

## Status Condition Type: Degraded 

The `Degraded` condition type is controlled by the add-on agent status controller. 
The status controller watches the `operator` deployment in the `hypershift` namespace on the managed cluster.

```
$ oc -n hypershift get deploy operator # run on the managed cluster
NAME       READY   UP-TO-DATE   AVAILABLE   AGE
operator   1/1     1            1           17m
```

If External DNS is enabled, 
the status controller watches the `external-dns` deployment in the `hypershift` namespace on the managed cluster.

```
$ oc -n hypershift get deploy external-dns # run on the managed cluster
NAME           READY   UP-TO-DATE   AVAILABLE   AGE
external-dns   1/1     1            1           17m
```

When the deployments are healthy, the `Degraded` condition should have the value of `False`.
When the `Degraded` condition value is `True`, check the `hypershift-addon` status output for reasons, 
and check the deployments in the `hypershift` namespace.

An example of a degraded add-on status is:

```
$ oc -n local-cluster get managedclusteraddons hypershift-addon -o yaml
...
status:
...
  - lastTransitionTime: "2022-11-30T15:21:49Z"
    message: There are no operator replica available
    reason: OperatorNotAllAvailableReplicas
    status: "True"
    type: Degraded
...
```

The above status indiciate the `operator` deployment in the `hypershift` namespace on the managed cluster is not healthy.

```
$ oc -n hypershift get deploy operator # # run on the managed cluster
NAME       READY   UP-TO-DATE   AVAILABLE   AGE
operator   0/0     0            0           35m
```

There are other unhealthy `Degraded` condition type reasons. Such as: 

- OperatorNotFound
- OperatorDeleted
- OperatorNotAllAvailableReplicas
- ExternalDNSNotFound
- ExternalDNSDeleted
- ExternalDNSNotAllAvailableReplicas

The above reasons reflect the nature of the degrade.
