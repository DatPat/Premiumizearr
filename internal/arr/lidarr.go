
package arr

import (
    "fmt"
    "time"
    "github.com/jackdallas/premiumizearr/pkg/premiumizeme"
    "golift.io/starr/lidarr"
)

func (l *LidarrArr) GetArrName() string {
    return l.Name
}

func (l *LidarrArr) HistoryContains(filename string) (int64, bool) {
    l.HistoryMutex.Lock()
    defer l.HistoryMutex.Unlock()

    if l.History == nil {
        return 0, false
    }

    for _, record := range l.History.Records {
        if CompareFileNamesFuzzy(record.SourceTitle, filename) {
            return record.ID, true
        }
    }
    return 0, false
}

func (l *LidarrArr) MarkHistoryItemAsFailed(id int64) error {
    l.ClientMutex.Lock()
    defer l.ClientMutex.Unlock()

    return l.Client.UpdateHistory(id, "failed")
}

func (l *LidarrArr) HandleErrorTransfer(transfer *premiumizeme.Transfer, historyId int64, client *premiumizeme.Premiumizeme) error {
    err := l.MarkHistoryItemAsFailed(historyId)
    if err != nil {
        return fmt.Errorf("failed to mark history item as failed: %v", err)
    }

    err = client.DeleteTransfer(transfer.ID)
    if err != nil {
        return fmt.Errorf("failed to delete transfer: %v", err)
    }

    return nil
}