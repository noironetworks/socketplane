package consul

import (
	"fmt"
	"sort"
	"time"

	"github.com/socketplane/socketplane/Godeps/_workspace/src/github.com/armon/go-metrics"
	"github.com/socketplane/socketplane/Godeps/_workspace/src/github.com/hashicorp/consul/consul/structs"
)

// Catalog endpoint is used to manipulate the service catalog
type Catalog struct {
	srv *Server
}

// Register is used register that a node is providing a given service.
func (c *Catalog) Register(args *structs.RegisterRequest, reply *struct{}) error {
	if done, err := c.srv.forward("Catalog.Register", args, args, reply); done {
		return err
	}
	defer metrics.MeasureSince([]string{"consul", "catalog", "register"}, time.Now())

	// Verify the args
	if args.Node == "" || args.Address == "" {
		return fmt.Errorf("Must provide node and address")
	}

	if args.Service != nil {
		// If no service id, but service name, use default
		if args.Service.ID == "" && args.Service.Service != "" {
			args.Service.ID = args.Service.Service
		}

		// Verify ServiceName provided if ID
		if args.Service.ID != "" && args.Service.Service == "" {
			return fmt.Errorf("Must provide service name with ID")
		}

		// Apply the ACL policy if any
		// The 'consul' service is excluded since it is managed
		// automatically internally.
		if args.Service.Service != ConsulServiceName {
			acl, err := c.srv.resolveToken(args.Token)
			if err != nil {
				return err
			} else if acl != nil && !acl.ServiceWrite(args.Service.Service) {
				c.srv.logger.Printf("[WARN] consul.catalog: Register of service '%s' on '%s' denied due to ACLs",
					args.Service.Service, args.Node)
				return permissionDeniedErr
			}
		}
	}

	if args.Check != nil {
		if args.Check.CheckID == "" && args.Check.Name != "" {
			args.Check.CheckID = args.Check.Name
		}
		if args.Check.Node == "" {
			args.Check.Node = args.Node
		}
	}

	_, err := c.srv.raftApply(structs.RegisterRequestType, args)
	if err != nil {
		c.srv.logger.Printf("[ERR] consul.catalog: Register failed: %v", err)
		return err
	}
	return nil
}

// Deregister is used to remove a service registration for a given node.
func (c *Catalog) Deregister(args *structs.DeregisterRequest, reply *struct{}) error {
	if done, err := c.srv.forward("Catalog.Deregister", args, args, reply); done {
		return err
	}
	defer metrics.MeasureSince([]string{"consul", "catalog", "deregister"}, time.Now())

	// Verify the args
	if args.Node == "" {
		return fmt.Errorf("Must provide node")
	}

	_, err := c.srv.raftApply(structs.DeregisterRequestType, args)
	if err != nil {
		c.srv.logger.Printf("[ERR] consul.catalog: Deregister failed: %v", err)
		return err
	}
	return nil
}

// ListDatacenters is used to query for the list of known datacenters
func (c *Catalog) ListDatacenters(args *struct{}, reply *[]string) error {
	c.srv.remoteLock.RLock()
	defer c.srv.remoteLock.RUnlock()

	// Read the known DCs
	var dcs []string
	for dc := range c.srv.remoteConsuls {
		dcs = append(dcs, dc)
	}

	// Sort the DCs
	sort.Strings(dcs)

	// Return
	*reply = dcs
	return nil
}

// ListNodes is used to query the nodes in a DC
func (c *Catalog) ListNodes(args *structs.DCSpecificRequest, reply *structs.IndexedNodes) error {
	if done, err := c.srv.forward("Catalog.ListNodes", args, args, reply); done {
		return err
	}

	// Get the local state
	state := c.srv.fsm.State()
	return c.srv.blockingRPC(&args.QueryOptions,
		&reply.QueryMeta,
		state.QueryTables("Nodes"),
		func() error {
			reply.Index, reply.Nodes = state.Nodes()
			return nil
		})
}

// ListServices is used to query the services in a DC
func (c *Catalog) ListServices(args *structs.DCSpecificRequest, reply *structs.IndexedServices) error {
	if done, err := c.srv.forward("Catalog.ListServices", args, args, reply); done {
		return err
	}

	// Get the current nodes
	state := c.srv.fsm.State()
	return c.srv.blockingRPC(&args.QueryOptions,
		&reply.QueryMeta,
		state.QueryTables("Services"),
		func() error {
			reply.Index, reply.Services = state.Services()
			return nil
		})
}

// ServiceNodes returns all the nodes registered as part of a service
func (c *Catalog) ServiceNodes(args *structs.ServiceSpecificRequest, reply *structs.IndexedServiceNodes) error {
	if done, err := c.srv.forward("Catalog.ServiceNodes", args, args, reply); done {
		return err
	}

	// Verify the arguments
	if args.ServiceName == "" {
		return fmt.Errorf("Must provide service name")
	}

	// Get the nodes
	state := c.srv.fsm.State()
	err := c.srv.blockingRPC(&args.QueryOptions,
		&reply.QueryMeta,
		state.QueryTables("ServiceNodes"),
		func() error {
			if args.TagFilter {
				reply.Index, reply.ServiceNodes = state.ServiceTagNodes(args.ServiceName, args.ServiceTag)
			} else {
				reply.Index, reply.ServiceNodes = state.ServiceNodes(args.ServiceName)
			}
			return nil
		})

	// Provide some metrics
	if err == nil {
		metrics.IncrCounter([]string{"consul", "catalog", "service", "query", args.ServiceName}, 1)
		if args.ServiceTag != "" {
			metrics.IncrCounter([]string{"consul", "catalog", "service", "query-tag", args.ServiceName, args.ServiceTag}, 1)
		}
		if len(reply.ServiceNodes) == 0 {
			metrics.IncrCounter([]string{"consul", "catalog", "service", "not-found", args.ServiceName}, 1)
		}
	}
	return err
}

// NodeServices returns all the services registered as part of a node
func (c *Catalog) NodeServices(args *structs.NodeSpecificRequest, reply *structs.IndexedNodeServices) error {
	if done, err := c.srv.forward("Catalog.NodeServices", args, args, reply); done {
		return err
	}

	// Verify the arguments
	if args.Node == "" {
		return fmt.Errorf("Must provide node")
	}

	// Get the node services
	state := c.srv.fsm.State()
	return c.srv.blockingRPC(&args.QueryOptions,
		&reply.QueryMeta,
		state.QueryTables("NodeServices"),
		func() error {
			reply.Index, reply.NodeServices = state.NodeServices(args.Node)
			return nil
		})
}
