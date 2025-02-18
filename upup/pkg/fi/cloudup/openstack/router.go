/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package openstack

import (
	"context"
	"fmt"

	"github.com/gophercloud/gophercloud/v2/openstack/networking/v2/extensions/layer3/routers"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/kops/util/pkg/vfs"
)

func (c *openstackCloud) ListRouters(opt routers.ListOpts) ([]routers.Router, error) {
	return listRouters(c, opt)
}

func listRouters(c OpenstackCloud, opt routers.ListOpts) ([]routers.Router, error) {
	var rs []routers.Router

	done, err := vfs.RetryWithBackoff(readBackoff, func() (bool, error) {
		allPages, err := routers.List(c.NetworkingClient(), opt).AllPages(context.TODO())
		if err != nil {
			return false, fmt.Errorf("error listing routers: %v", err)
		}

		r, err := routers.ExtractRouters(allPages)
		if err != nil {
			return false, fmt.Errorf("error extracting routers from pages: %v", err)
		}
		rs = r
		return true, nil
	})
	if err != nil {
		return rs, err
	} else if done {
		return rs, nil
	} else {
		return rs, wait.ErrWaitTimeout
	}
}

func (c *openstackCloud) CreateRouter(opt routers.CreateOptsBuilder) (*routers.Router, error) {
	return createRouter(c, opt)
}

func createRouter(c OpenstackCloud, opt routers.CreateOptsBuilder) (*routers.Router, error) {
	var r *routers.Router

	done, err := vfs.RetryWithBackoff(writeBackoff, func() (bool, error) {
		v, err := routers.Create(context.TODO(), c.NetworkingClient(), opt).Extract()
		if err != nil {
			return false, fmt.Errorf("error creating router: %v", err)
		}
		r = v
		return true, nil
	})
	if err != nil {
		return r, err
	} else if done {
		return r, nil
	} else {
		return r, wait.ErrWaitTimeout
	}
}

func (c *openstackCloud) CreateRouterInterface(routerID string, opt routers.AddInterfaceOptsBuilder) (*routers.InterfaceInfo, error) {
	return createRouterInterface(c, routerID, opt)
}

func createRouterInterface(c OpenstackCloud, routerID string, opt routers.AddInterfaceOptsBuilder) (*routers.InterfaceInfo, error) {
	var i *routers.InterfaceInfo

	done, err := vfs.RetryWithBackoff(writeBackoff, func() (bool, error) {
		v, err := routers.AddInterface(context.TODO(), c.NetworkingClient(), routerID, opt).Extract()
		if err != nil {
			return false, fmt.Errorf("error creating router interface: %v", err)
		}
		i = v
		return true, nil
	})
	if err != nil {
		return i, err
	} else if done {
		return i, nil
	} else {
		return i, wait.ErrWaitTimeout
	}
}

func (c *openstackCloud) DeleteRouterInterface(routerID string, opt routers.RemoveInterfaceOptsBuilder) error {
	return deleteRouterInterface(c, routerID, opt)
}

func deleteRouterInterface(c OpenstackCloud, routerID string, opt routers.RemoveInterfaceOptsBuilder) error {
	done, err := vfs.RetryWithBackoff(deleteBackoff, func() (bool, error) {
		_, err := routers.RemoveInterface(context.TODO(), c.NetworkingClient(), routerID, opt).Extract()
		if err != nil && !isNotFound(err) {
			return false, fmt.Errorf("error deleting router interface: %v", err)
		}
		if isNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	} else if done {
		return nil
	} else {
		return wait.ErrWaitTimeout
	}
}

func (c *openstackCloud) DeleteRouter(routerID string) error {
	return deleteRouter(c, routerID)
}

func deleteRouter(c OpenstackCloud, routerID string) error {
	done, err := vfs.RetryWithBackoff(deleteBackoff, func() (bool, error) {
		err := routers.Delete(context.TODO(), c.NetworkingClient(), routerID).ExtractErr()
		if err != nil && !isNotFound(err) {
			return false, fmt.Errorf("error deleting router: %v", err)
		}
		if isNotFound(err) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	} else if done {
		return nil
	} else {
		return wait.ErrWaitTimeout
	}
}
