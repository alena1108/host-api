package healthcheck

import (
	"fmt"
	"strings"
	"time"

	"github.com/Sirupsen/logrus"
	lru "github.com/hashicorp/golang-lru"
	"github.com/rancherio/go-rancher/client"
	"github.com/rancherio/host-api/pkg/haproxy"
	"github.com/rancherio/host-api/util"
)

var PREFIX = "cattle-"
var SERVER_NAME = "svname"
var STATUS = "status"

func Poll() error {
	client, err := util.GetRancherClient()
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("Can not create RancherClient, No credentials found")
	}
	c, _ := lru.New(500)
	m := &Monitor{
		client:         client,
		reportedStatus: c,
	}

	for stat := range m.getStats() {
		m.processStat(stat)
	}

	return nil
}

type Monitor struct {
	client         *client.RancherClient
	reportedStatus *lru.Cache
}

func (m *Monitor) getStats() <-chan haproxy.Stat {
	c := make(chan haproxy.Stat)
	go m.readStats(c)
	return c
}

func (m *Monitor) readStats(c chan<- haproxy.Stat) {
	defer close(c)

	count := 0

	h := &haproxy.Monitor{
		SocketPath: haproxy.HAPROXY_SOCK,
	}

	for {
		// Sleep up front.  This way if this program gets restarted really fast we don't spam cattle
		time.Sleep(2 * time.Second)

		stats, err := h.Stats()
		currentCount := 0
		if err != nil {
			logrus.Errorf("Failed to read stats: %v", err)
			continue
		}

		for _, stat := range stats {
			if strings.HasPrefix(stat[SERVER_NAME], PREFIX) {
				currentCount++
				c <- stat
			}
		}

		if currentCount != count {
			count = currentCount
			logrus.Infof("Monitoring %d backends", count)
		}

	}
}

func (m *Monitor) processStat(stat haproxy.Stat) {
	serverName := strings.TrimPrefix(stat[SERVER_NAME], PREFIX)
	currentStatus := stat[STATUS]

	previousStatus, _ := m.reportedStatus.Get(serverName)
	if strings.HasPrefix(currentStatus, "UP ") {
		// do nothing on partial UP
		return
	}

	if currentStatus == "UP" && previousStatus != "UP" && previousStatus != "INIT" {
		currentStatus = "INIT"
	}

	if previousStatus != currentStatus {
		err := m.reportStatus(serverName, currentStatus)
		if err != nil {
			logrus.Errorf("Failed to report status %s=%s: %v", serverName, currentStatus, err)
		} else {
			m.reportedStatus.Add(serverName, currentStatus)
		}
	}
}

func (m *Monitor) reportStatus(serverName, currentStatus string) error {
	_, err := m.client.ServiceEvent.Create(&client.ServiceEvent{
		HealthcheckUuid:   serverName,
		ReportedHealth:    currentStatus,
		ExternalTimestamp: time.Now().Unix(),
	})

	if err != nil {
		return err
	}

	logrus.Infof("%s=%s", serverName, currentStatus)
	return nil
}
