package service

import (
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jackdallas/premiumizearr/internal/config"
	"github.com/jackdallas/premiumizearr/internal/progress_downloader"
	"github.com/jackdallas/premiumizearr/internal/utils"
	"github.com/jackdallas/premiumizearr/pkg/premiumizeme"
	log "github.com/sirupsen/logrus"
)

type DownloadDetails struct {
	Added              time.Time
	Name               string
	ProgressDownloader *progress_downloader.WriteCounter
}

type TransferManagerService struct {
	premiumizemeClient *premiumizeme.Premiumizeme
	arrsManager        *ArrsManagerService
	config             *config.Config
	lastUpdated        int64
	transfers          []premiumizeme.Transfer
	runningTask        bool
	downloadListMutex  *sync.Mutex
	downloadList       map[string]*DownloadDetails
	status             string
	downloadsFolderID  string
}

// Handle
func (t TransferManagerService) New() TransferManagerService {
	t.premiumizemeClient = nil
	t.arrsManager = nil
	t.config = nil
	t.lastUpdated = time.Now().Unix()
	t.transfers = make([]premiumizeme.Transfer, 0)
	t.runningTask = false
	t.downloadListMutex = &sync.Mutex{}
	t.downloadList = make(map[string]*DownloadDetails, 0)
	t.status = ""
	t.downloadsFolderID = ""
	return t
}

func (t *TransferManagerService) Init(pme *premiumizeme.Premiumizeme, arrsManager *ArrsManagerService, config *config.Config) {
	t.premiumizemeClient = pme
	t.arrsManager = arrsManager
	t.config = config
	t.CleanUpUnzipDir()
}

func (t *TransferManagerService) CleanUpUnzipDir() {
	log.Info("Cleaning unzip directory")

	unzipBase, err := t.config.GetUnzipBaseLocation()
	if err != nil {
		log.Errorf("Error getting unzip base location: %s", err.Error())
		return
	}

	err = utils.RemoveContents(unzipBase)
	if err != nil {
		log.Errorf("Error cleaning unzip directory: %s", err.Error())
		return
	}

}

func (manager *TransferManagerService) ConfigUpdatedCallback(currentConfig config.Config, newConfig config.Config) {
	if currentConfig.UnzipDirectory != newConfig.UnzipDirectory {
		manager.CleanUpUnzipDir()
	}
}

func (manager *TransferManagerService) Run(interval time.Duration) {
	manager.downloadsFolderID = utils.GetDownloadsFolderIDFromPremiumizeme(manager.premiumizemeClient)
	for {
		manager.runningTask = true
		manager.TaskUpdateTransfersList()
		manager.TaskCheckPremiumizeDownloadsFolder()
		manager.runningTask = false
		manager.lastUpdated = time.Now().Unix()
		time.Sleep(interval)
	}
}

func (manager *TransferManagerService) GetDownloads() map[string]*DownloadDetails {
	return manager.downloadList
}

func (manager *TransferManagerService) GetTransfers() *[]premiumizeme.Transfer {
	return &manager.transfers
}
func (manager *TransferManagerService) GetStatus() string {
	return manager.status
}

func (manager *TransferManagerService) TaskUpdateTransfersList() {
	log.Debug("Running Task UpdateTransfersList")
	transfers, err := manager.premiumizemeClient.GetTransfers()
	if err != nil {
		log.Errorf("Error getting transfers: %s", err.Error())
		return
	}
	manager.updateTransfers(transfers)

	log.Tracef("Checking %d transfers against %d Arr clients", len(transfers), len(manager.arrsManager.GetArrs()))
	for _, transfer := range transfers {
		found := false
		for _, arr := range manager.arrsManager.GetArrs() {
			if found {
				break
			}
			if transfer.Status == "error" {
				log.Tracef("Checking errored transfer %s against %s history", transfer.Name, arr.GetArrName())
				arrID, contains := arr.HistoryContains(transfer.Name)
				if !contains {
					log.Tracef("%s history doesn't contain %s", arr.GetArrName(), transfer.Name)
					continue
				}
				log.Tracef("Found %s in %s history", transfer.Name, arr.GetArrName())
				found = true
				log.Debugf("Processing transfer that has errored: %s", transfer.Name)
				go arr.HandleErrorTransfer(&transfer, arrID, manager.premiumizemeClient)

			}
		}
	}
}

func (manager *TransferManagerService) TaskCheckPremiumizeDownloadsFolder() {
	log.Debug("Running Task CheckPremiumizeDownloadsFolder")

	items, err := manager.premiumizemeClient.ListFolder(manager.downloadsFolderID)
	if err != nil {
		log.Errorf("Error listing downloads folder: %s", err.Error())
		return
	}

	for _, item := range items {
		if manager.countDownloads() < manager.config.SimultaneousDownloads {
			log.Debugf("Processing completed item: %s", item.Name)
			manager.HandleFinishedItem(item, manager.config.DownloadsDirectory)
		} else {
			log.Debugf("Not processing any more transfers, %d are running and cap is %d", manager.countDownloads(), manager.config.SimultaneousDownloads)
			break
		}
	}
}

func (manager *TransferManagerService) updateTransfers(transfers []premiumizeme.Transfer) {
	manager.transfers = transfers
}

func (manager *TransferManagerService) addDownload(item *premiumizeme.Item) {
	manager.downloadListMutex.Lock()
	defer manager.downloadListMutex.Unlock()

	manager.downloadList[item.Name] = &DownloadDetails{
		Added:              time.Now(),
		Name:               item.Name,
		ProgressDownloader: progress_downloader.NewWriteCounter(),
	}
}

func (manager *TransferManagerService) countDownloads() int {
	manager.downloadListMutex.Lock()
	defer manager.downloadListMutex.Unlock()

	return len(manager.downloadList)
}

func (manager *TransferManagerService) removeDownload(name string) {
	manager.downloadListMutex.Lock()
	defer manager.downloadListMutex.Unlock()

	delete(manager.downloadList, name)
}

func (manager *TransferManagerService) downloadExists(itemName string) bool {
	manager.downloadListMutex.Lock()
	defer manager.downloadListMutex.Unlock()

	for _, dl := range manager.downloadList {
		if dl.Name == itemName {
			return true
		}
	}

	return false
}

// Returns when the download has been added to the list
func (manager *TransferManagerService) HandleFinishedItem(item premiumizeme.Item, downloadDirectory string) {
	if manager.downloadExists(item.Name) {
		log.Tracef("Transfer %s is already downloading", item.Name)
		return
	}

	manager.addDownload(&item)

	go func() {
		log.Debug("Downloading: ", item.Name)
		log.Tracef("%+v", item)
		var link string
		var err error
		if item.Type == "file" {
			link, err = manager.premiumizemeClient.GenerateZippedFileLink(item.ID)
		} else if item.Type == "folder" {
			link, err = manager.premiumizemeClient.GenerateZippedFolderLink(item.ID)
		} else {
			log.Errorf("Item is not of type 'file' or 'folder' !! Can't download %s", item.Name)
			return
		}
		if err != nil {
			log.Error("Error generating download link: %s", err)
			manager.removeDownload(item.Name)
			return
		}
		log.Trace("Downloading from: ", link)

		tempDir, err := manager.config.GetNewUnzipLocation()
		if err != nil {
			log.Errorf("Could not create temp dir: %s", err)
			manager.removeDownload(item.Name)
			return
		}

		splitString := strings.Split(link, "/")
		savePath := path.Join(tempDir, splitString[len(splitString)-1])
		log.Trace("Downloading to: ", savePath)

		out, err := os.Create(savePath)
		if err != nil {
			log.Errorf("Could not create save path: %s", err)
			manager.removeDownload(item.Name)
			return
		}
		defer out.Close()

		err = progress_downloader.DownloadFile(link, savePath, manager.downloadList[item.Name].ProgressDownloader)

		if err != nil {
			log.Errorf("Could not download file: %s", err)
			manager.removeDownload(item.Name)
			return
		}

		log.Tracef("Unzipping %s to %s", savePath, downloadDirectory)
		err = utils.Unzip(savePath, downloadDirectory)
		if err != nil {
			log.Errorf("Could not unzip file: %s", err)
			manager.removeDownload(item.Name)
			return
		}

		log.Tracef("Removing zip %s from system", savePath)
		err = os.RemoveAll(savePath)
		if err != nil {
			manager.removeDownload(item.Name)
			log.Errorf("Could not remove zip: %s", err)
			return
		}

		err = manager.premiumizemeClient.DeleteFolder(item.ID)
		if err != nil {
			err = manager.premiumizemeClient.DeleteFile(item.ID)
			if err != nil {
				manager.removeDownload(item.Name)
				log.Error("[%s]error deleting folder from premiumize.me: %s", item.Name, err)
				return
			}
		}

		//Remove download entry from downloads map
		manager.removeDownload(item.Name)
	}()
}
