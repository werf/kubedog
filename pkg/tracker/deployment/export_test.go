package deployment

import appsv1 "k8s.io/api/apps/v1"

func (d *Tracker) TestOnlyInjectReplicaSetAdded(rs *appsv1.ReplicaSet) {
	d.replicaSetAdded <- rs
}

func (d *Tracker) TestOnlyInjectReplicaSetModified(rs *appsv1.ReplicaSet) {
	d.replicaSetModified <- rs
}

func (d *Tracker) TestOnlyInjectReplicaSetDeleted(rs *appsv1.ReplicaSet) {
	d.replicaSetDeleted <- rs
}

func (d *Tracker) TestOnlyInjectReplicaSetUnselected(rs *appsv1.ReplicaSet) {
	d.replicaSetUnselected <- rs
}

func (d *Tracker) TestOnlyReplicaSetDeletedChanCap() int {
	return cap(d.replicaSetDeleted)
}

func (d *Tracker) TestOnlyInjectResourceFailure(reason string) {
	d.resourceFailed <- reason
}
