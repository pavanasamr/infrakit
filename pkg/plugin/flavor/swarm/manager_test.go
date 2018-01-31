package swarm

import (
	"fmt"
	"testing"

	docker_types "github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/swarm"
	mock_client "github.com/docker/infrakit/pkg/mock/docker/docker/client"
	"github.com/docker/infrakit/pkg/spi/group"
	"github.com/docker/infrakit/pkg/spi/instance"
	"github.com/docker/infrakit/pkg/types"
	"github.com/docker/infrakit/pkg/util/docker"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

func TestManagerDrain(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	selfAddr := "10.20.100.1"
	self := instance.LogicalID(selfAddr)
	managerStop := make(chan struct{})

	client := mock_client.NewMockAPIClientCloser(ctrl)

	flavorImpl := NewManagerFlavor(scp, func(Spec) (docker.APIClientCloser, error) {
		return client, nil
	}, templ(DefaultManagerInitScriptTemplate), managerStop, &self)

	version := swarm.Version{Index: uint64(9999)}
	swarmInfo := swarm.Swarm{
		ClusterInfo: swarm.ClusterInfo{
			ID: "ClusterUUID",
			Meta: swarm.Meta{
				Version: version,
			},
		},
		JoinTokens: swarm.JoinTokens{
			Manager: "ManagerToken",
			Worker:  "WorkerToken",
		},
	}

	client.EXPECT().Close().AnyTimes()
	client.EXPECT().SwarmInspect(gomock.Any()).Return(swarmInfo, nil).AnyTimes()
	client.EXPECT().Info(gomock.Any()).Return(infoResponse, nil).AnyTimes()

	flavorProperties := types.AnyString(`
{
  "Attachments" : {
    "10.20.100.1" : [ { "ID" : "disk01", "Type" : "disk" }, { "ID" : "nic01", "Type" : "nic" } ],
    "10.20.100.2" : [ { "ID" : "disk02", "Type" : "disk" }, { "ID" : "nic02", "Type" : "nic" } ],
    "10.20.100.3" : [ { "ID" : "disk03", "Type" : "disk" }, { "ID" : "nic03", "Type" : "nic" } ]
  }
}
`)
	index := group.Index{Group: group.ID("group"), Sequence: 0}
	id := self

	// manager self info
	nodeInfo := swarm.Node{ManagerStatus: &swarm.ManagerStatus{Addr: selfAddr}}
	client.EXPECT().NodeInspectWithRaw(gomock.Any(), nodeID).Return(nodeInfo, nil, nil)

	details, err := flavorImpl.Prepare(flavorProperties,
		instance.Spec{Tags: map[string]string{"a": "b"}, LogicalID: &id},
		group.AllocationMethod{LogicalIDs: []instance.LogicalID{id}},
		index)
	require.NoError(t, err)

	link := types.NewLinkFromMap(details.Tags)
	associationID := link.Value()
	associationTag := link.Label()

	filter, err := filters.FromParam(fmt.Sprintf(`{"label": {"%s=%s": true}}`, associationTag, associationID))
	require.NoError(t, err)

	// Do a drain
	swarmNodeID := "swarm-id-1"
	client.EXPECT().NodeList(gomock.Any(),
		docker_types.NodeListOptions{Filters: filter}).Return(
		[]swarm.Node{
			{ID: swarmNodeID},
		},
		nil)
	client.EXPECT().NodeInspectWithRaw(gomock.Any(), swarmNodeID).Return(
		swarm.Node{
			ID:   swarmNodeID,
			Spec: swarm.NodeSpec{Role: swarm.NodeRoleManager},
		},
		nil,
		nil,
	)
	client.EXPECT().NodeUpdate(gomock.Any(), swarmNodeID, version, swarm.NodeSpec{Role: swarm.NodeRoleWorker}).Return(nil)

	// Because this is the self node....
	client.EXPECT().SwarmLeave(gomock.Any(), true)

	err = flavorImpl.Drain(flavorProperties,
		instance.Description{
			LogicalID: &id,
			Tags:      map[string]string{associationTag: associationID},
		})
	require.NoError(t, err)

	close(managerStop)
}
