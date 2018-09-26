// Copyright 2018 VMware, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package node

import (
	"errors"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
)

// ErrNodeAlreadyExists is returned if there's exists a node with
// the same name but different UUID.
var ErrNodeAlreadyExists = errors.New("another node with the same name exists")

// Cache provides thread-safe functionality to cache node information. Note that
// node names are handled in a case sensitive manner, and must be unique.
type Cache interface {
	// DeleteNodeByUUID deletes a node entry by its UUID and returns its current name.
	DeleteNodeByUUID(nodeUUID string) (string, error)
	// DeleteNodeByName deletes a node entry by its name and returns its current UUID.
	DeleteNodeByName(nodeName string) (string, error)
	// LoadNodeNameByUUID returns a node's name given its UUID.
	LoadNodeNameByUUID(nodeUUID string) (string, error)
	// LoadNodeUUIDByName returns a node's UUID given its name.
	LoadNodeUUIDByName(nodeName string) (string, error)
	// Range calls f sequentially for each node entry.
	Range(f func(nodeUUID, nodeName string) bool)
	// Store associates the node UUID with its name. If the node UUID already
	// exists in the Cache, the name associated with it is updated.
	Store(nodeUUID, nodeName string) error
}

var (
	// cacheInstance is a Cache singleton.
	cacheInstance *defaultCache
	// onceForCache is used for initializing the Cache singleton.
	onceForCache sync.Once
)

// GetCache returns the Cache singleton.
func GetCache() Cache {
	onceForCache.Do(func() {
		log.Info("Initializing node.defaultCache")
		cacheInstance = &defaultCache{
			mutex:        sync.Mutex{},
			uuidsToNames: make(map[string]string),
			namesToUUIDs: make(map[string]string),
		}
		log.Info("node.defaultCache initialized")
	})
	return cacheInstance
}

// defaultCache caches node information and provides functionality around it.
type defaultCache struct {
	// mutex is used to ensure atomicity.
	mutex sync.Mutex
	// uuidsToNames map node UUIDs to their names.
	uuidsToNames map[string]string
	// namesToUUIDs map node names to their UUIDs.
	namesToUUIDs map[string]string
}

func normalizeUUID(nodeUUID string) string {
	return strings.ToLower(nodeUUID)
}

func (c *defaultCache) DeleteNodeByUUID(nodeUUID string) (string, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	nodeUUID = normalizeUUID(nodeUUID)
	nodeName, exists := c.uuidsToNames[nodeUUID]
	if !exists {
		log.WithField("nodeUUID", nodeUUID).Warn("Node entry wasn't found")
		return "", ErrNodeNotFound
	}

	delete(c.uuidsToNames, nodeUUID)
	delete(c.namesToUUIDs, nodeName)
	log.WithFields(log.Fields{
		"nodeUUID": nodeUUID, "nodeName": nodeName,
	}).Info("Node entry was deleted")

	return nodeName, nil
}

func (c *defaultCache) DeleteNodeByName(nodeName string) (string, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	nodeUUID, exists := c.namesToUUIDs[nodeName]
	if !exists {
		log.WithField("nodeName", nodeName).Warn("Node entry wasn't found")
		return "", ErrNodeNotFound
	}

	delete(c.namesToUUIDs, nodeName)
	delete(c.uuidsToNames, nodeUUID)
	log.WithFields(log.Fields{
		"nodeName": nodeName, "nodeUUID": nodeUUID,
	}).Info("Node entry was deleted")

	return nodeUUID, nil
}

func (c *defaultCache) LoadNodeNameByUUID(nodeUUID string) (string, error) {
	nodeUUID = normalizeUUID(nodeUUID)
	c.mutex.Lock()
	nodeName, exists := c.uuidsToNames[nodeUUID]
	c.mutex.Unlock()

	if !exists {
		log.WithField("nodeUUID", nodeUUID).Warn("Node entry wasn't found")
		return "", ErrNodeNotFound
	}

	log.WithFields(log.Fields{
		"nodeUUID": nodeUUID, "nodeName": nodeName,
	}).Info("Node entry was loaded")
	return nodeName, nil
}

func (c *defaultCache) LoadNodeUUIDByName(nodeName string) (string, error) {
	c.mutex.Lock()
	nodeUUID, exists := c.namesToUUIDs[nodeName]
	c.mutex.Unlock()

	if !exists {
		log.WithField("nodeName", nodeName).Warn("Node entry wasn't found")
		return "", ErrNodeNotFound
	}

	log.WithFields(log.Fields{
		"nodeName": nodeName, "nodeUUID": nodeUUID,
	}).Info("Node entry was loaded")
	return nodeUUID, nil
}

func (c *defaultCache) Range(f func(string, string) bool) {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	for nodeUUID, nodeName := range c.uuidsToNames {
		log.WithFields(log.Fields{
			"nodeName": nodeName, "nodeUUID": nodeUUID,
		}).Info("Calling f func for node entry")

		if !f(nodeUUID, nodeName) {
			log.WithFields(log.Fields{
				"nodeName": nodeName, "nodeUUID": nodeUUID,
			}).Info("f func returned false for node entry, breaking")
			break
		}
	}
}

func (c *defaultCache) Store(nodeUUID, nodeName string) error {
	c.mutex.Lock()
	defer c.mutex.Unlock()

	// Return an error if there exists a node with the same name but different UUID.
	nodeUUID = normalizeUUID(nodeUUID)
	prevNameForUUID, prevNameExistsForUUID := c.uuidsToNames[nodeUUID]
	prevUUIDForName, prevUUIDExistsForName := c.namesToUUIDs[nodeName]
	if prevNameExistsForUUID && prevUUIDExistsForName && prevUUIDForName != nodeUUID {
		log.WithFields(log.Fields{
			"nodeUUID": nodeUUID, "prevUUIDForName": prevUUIDForName, "nodeName": nodeName,
		}).Error("Another node with the same name exists")
		return ErrNodeAlreadyExists
	}

	// Clear cache entry for the given UUID and name, along with previously associated values.
	delete(c.uuidsToNames, nodeUUID)
	delete(c.namesToUUIDs, nodeName)
	if prevNameExistsForUUID {
		delete(c.namesToUUIDs, prevNameForUUID)
	}
	if prevUUIDExistsForName {
		delete(c.uuidsToNames, prevUUIDForName)
	}

	// Store the new node UUID and name.
	c.uuidsToNames[nodeUUID] = nodeName
	c.namesToUUIDs[nodeName] = nodeUUID
	log.WithFields(log.Fields{
		"nodeUUID": nodeUUID, "nodeName": nodeName,
	}).Info("Node entry was stored")
	return nil
}
