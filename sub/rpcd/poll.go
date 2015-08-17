package rpcd

import (
	"github.com/Symantec/Dominator/proto/sub"
)

func (t *rpcType) Poll(request sub.PollRequest, reply *sub.PollResponse) error {
	var response sub.PollResponse
	rwLock.RLock()
	response.FetchInProgress = fetchInProgress
	response.UpdateInProgress = updateInProgress
	rwLock.RUnlock()
	fs := fileSystemHistory.FileSystem()
	if fs != nil &&
		request.HaveGeneration != fileSystemHistory.GenerationCount() {
		response.GenerationCount = fileSystemHistory.GenerationCount()
		response.FileSystem = fs
	}
	*reply = response
	return nil
}