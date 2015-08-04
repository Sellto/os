package docker

import (
	"fmt"

	"github.com/docker/libcompose/docker"
	"github.com/docker/libcompose/project"
	"github.com/docker/machine/log"
	"github.com/rancherio/os/config"
	"github.com/samalba/dockerclient"
)

type Service struct {
	*docker.Service
	deps    map[string][]string
	context *docker.Context
}

func NewService(factory *ServiceFactory, name string, serviceConfig *project.ServiceConfig, context *docker.Context) *Service {
	return &Service{
		Service: docker.NewService(name, serviceConfig, context),
		deps:    factory.Deps,
		context: context,
	}
}

func (s *Service) DependentServices() []project.ServiceRelationship {
	rels := s.Service.DependentServices()
	for _, dep := range s.deps[s.Name()] {
		rels = appendLink(rels, dep, true)
	}

	if s.requiresSyslog() {
		rels = appendLink(rels, "syslog", false)
	}

	if s.requiresUserDocker() {
		// Linking to cloud-init is a hack really.  The problem is we need to link to something
		// that will trigger a reload
		rels = appendLink(rels, "cloud-init", false)
	} else if s.missingImage() {
		rels = appendLink(rels, "network", false)
	}
	return rels
}

func (s *Service) missingImage() bool {
	image := s.Config().Image
	if image == "" {
		return false
	}
	client := s.context.ClientFactory.Create(s)
	i, err := client.InspectImage(s.Config().Image)
	return err != nil || i == nil
}

func (s *Service) requiresSyslog() bool {
	return s.Config().LogDriver == "syslog"
}

func (s *Service) requiresUserDocker() bool {
	return s.Config().Labels.MapParts()[config.SCOPE] != config.SYSTEM
}

func appendLink(deps []project.ServiceRelationship, name string, optional bool) []project.ServiceRelationship {
	rel := project.NewServiceRelationship(name, project.REL_TYPE_LINK)
	rel.Optional = optional
	return append(deps, rel)
}

func (s *Service) Up() error {
	labels := s.Config().Labels.MapParts()

	if err := s.Service.Create(); err != nil {
		return err
	}
	if err := s.rename(); err != nil {
		return err
	}
	if labels[config.CREATE_ONLY] == "true" {
		return s.checkReload(labels)
	}
	if err := s.Service.Up(); err != nil {
		return err
	}
	if labels[config.DETACH] == "false" {
		if err := s.wait(); err != nil {
			return err
		}
	}

	return s.checkReload(labels)
}

func (s *Service) checkReload(labels map[string]string) error {
	if labels[config.RELOAD_CONFIG] == "true" {
		return project.ErrRestart
	}
	return nil
}

func (s *Service) Create() error {
	if err := s.Service.Create(); err != nil {
		return err
	}
	return s.rename()
}

func (s *Service) getContainer() (dockerclient.Client, *dockerclient.ContainerInfo, error) {
	containers, err := s.Service.Containers()
	if err != nil {
		return nil, nil, err
	}

	if len(containers) == 0 {
		return nil, nil, nil
	}

	id, err := containers[0].Id()
	if err != nil {
		return nil, nil, err
	}

	client := s.context.ClientFactory.Create(s)
	info, err := client.InspectContainer(id)
	return client, info, err
}

func (s *Service) wait() error {
	client, info, err := s.getContainer()
	if err != nil || info == nil {
		return err
	}

	status := <-client.Wait(info.Id)
	if status.Error != nil {
		return status.Error
	}

	if status.ExitCode == 0 {
		return nil
	} else {
		return fmt.Errorf("ExitCode %d", status.ExitCode)
	}
}

func (s *Service) rename() error {
	client, info, err := s.getContainer()
	if err != nil || info == nil {
		return err
	}

	if len(info.Name) > 0 && info.Name[1:] != s.Name() {
		log.Debugf("Renaming container %s => %s", info.Name[1:], s.Name())
		return client.RenameContainer(info.Name[1:], s.Name())
	} else {
		return nil
	}
}
