package deployment

import appsv1 "k8s.io/api/apps/v1"

func (d *Tracker) TestOnlyInjectReplicaSetModified(rs *appsv1.ReplicaSet) {
	d.replicaSetModified <- rs
}

func (d *Tracker) TestOnlyInjectReplicaSetDeleted(rs *appsv1.ReplicaSet) {
	d.replicaSetDeleted <- rs
}
