// Copyright 2023 OnMetal authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package provisioner

import (
	"context"
	"fmt"
	"sync"

	"github.com/ceph/go-ceph/rados"
	"github.com/onmetal/cephlet/ori/volume/server"
	"github.com/onmetal/onmetal-image/utils/sets"
)

func New(auth *Credentials, config *CephConfig) *Provisioner {
	config.Defaults()
	return &Provisioner{
		auth: auth,
		cfg:  config,

		connLock: sync.Mutex{},

		inProgress:     map[string]sets.Empty{},
		inProgressLock: sync.Mutex{},
	}
}

type Provisioner struct {
	auth *Credentials
	cfg  *CephConfig

	conn     *rados.Conn
	connLock sync.Mutex

	inProgress     sets.Set[string]
	inProgressLock sync.Mutex
}

func (p *Provisioner) Lock(name string) error {
	p.inProgressLock.Lock()
	defer p.inProgressLock.Unlock()

	if p.inProgress.Has(name) {
		return fmt.Errorf("failed to acquire lock: %s already in use", name)
	}

	p.inProgress.Insert(name)

	return nil
}

func (p *Provisioner) Release(name string) {
	p.inProgressLock.Lock()
	defer p.inProgressLock.Unlock()

	p.inProgress.Delete(name)
}

func (p *Provisioner) MappingExists(ctx context.Context, volume *server.CephVolume) (bool, error) {
	if err := p.reconnect(ctx); err != nil {
		return false, fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return false, fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	idMap, err := ioCtx.GetAllOmapValues(p.cfg.OmapNameMappings, "", volume.Requested.Name, 10)
	if err != nil {
		return false, fmt.Errorf("unable to get omap: %w", err)
	}

	id, found := idMap[volume.Requested.Name]
	if !found {
		return false, nil
	}

	volume.ImageId = string(id)

	return true, nil
}

func (p *Provisioner) PutMapping(ctx context.Context, volume *server.CephVolume) error {
	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := ioCtx.SetOmap(p.cfg.OmapNameMappings, map[string][]byte{
		volume.Requested.Name: []byte(volume.ImageId),
	}); err != nil {
		return fmt.Errorf("unablet to set omap values: %w", err)
	}

	return nil
}

func (p *Provisioner) DeleteMapping(ctx context.Context, volume *server.CephVolume) error {
	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	if err := ioCtx.RmOmapKeys(p.cfg.OmapNameMappings, []string{volume.Requested.Name}); err != nil {
		return fmt.Errorf("unablet to delete mapping omap value: %w", err)
	}

	return nil
}

func (p *Provisioner) CreateCephImage(ctx context.Context, volume *server.CephVolume) error {
	//TODO implement me
	return fmt.Errorf("implement me")
}

func (p *Provisioner) UpdateCephImage(ctx context.Context, volume *server.CephVolume) error {
	if err := p.reconnect(ctx); err != nil {
		return fmt.Errorf("unable to reconnect: %w", err)
	}

	ioCtx, err := p.conn.OpenIOContext(p.cfg.Pool)
	if err != nil {
		return fmt.Errorf("unable to get io context: %w", err)
	}
	defer ioCtx.Destroy()

	attributeMap, err := ioCtx.GetAllOmapValues(p.cfg.OmapNameVolumes, "", volume.ImageId, 10)
	if err != nil {
		return fmt.Errorf("unable to get attribute omap: %w", err)
	}

	_ = attributeMap
	return nil
}

func (p *Provisioner) DeleteCephImage(ctx context.Context, volume *server.CephVolume) error {
	//TODO implement me
	return fmt.Errorf("implement me")
}

func (p *Provisioner) reconnect(ctx context.Context) error {
	p.connLock.Lock()
	defer p.connLock.Unlock()

	if p.conn == nil {
		return p.connect(ctx)
	}

	_, err := p.conn.ListPools()
	if err != nil {
		return p.connect(ctx)
	}

	return nil
}

func (p *Provisioner) connect(ctx context.Context) error {
	conn, err := p.auth.Connect(ctx)
	if err != nil {
		return err
	}
	p.conn = conn

	return nil
}
