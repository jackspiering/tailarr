package deploy

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/jackspiering/tailarr/internal/scaletail"
)

// Health is a coarse container/service health state.
type Health string

const (
	HealthHealthy   Health = "healthy"
	HealthStarting  Health = "starting"
	HealthUnhealthy Health = "unhealthy"
	HealthRunning   Health = "running/no-healthcheck"
	HealthExited    Health = "exited"
	HealthUnknown   Health = "unknown"
	HealthStopped   Health = "stopped"
)

// Container is one Docker container that belongs to a deployed service.
type Container struct {
	Name   string
	State  string
	Status string
	Health Health
}

// ServiceStatus is the live state of one deployed service.
type ServiceStatus struct {
	Name    string
	Managed bool
	// Health is the worst health of the containers, or stopped when the
	// service has none.
	Health     Health
	Containers []Container
}

// Overview lists the deployed services and their containers.
type Overview struct {
	// Services lists every deployed service with its containers, by name.
	Services []ServiceStatus
	// DockerErr says why docker ps failed. Health is unknown then.
	DockerErr string
}

// CollectOverview builds a status overview for the deploy path from a single
// `docker ps -a` pass.
func CollectOverview(deployPath string) (Overview, error) {
	var st Overview
	deployed, err := scaletail.ListDeployed(deployPath)
	if err != nil {
		return st, err
	}
	rows, perr := dockerPS()
	if perr != nil {
		st.DockerErr = perr.Error()
	}
	for _, s := range deployed {
		svc := ServiceStatus{Name: s.Name, Managed: IsManaged(s.Dir), Health: HealthUnknown}
		if perr == nil {
			svc.Containers, svc.Health = serviceContainers(rows, s.Name)
		}
		st.Services = append(st.Services, svc)
	}
	return st, nil
}

// psRow is one line of `docker ps -a` output.
type psRow struct {
	name, state, status, label string
}

// dockerPS lists every container once. The tailarr.service label maps a
// container to its service even when the template renames it.
func dockerPS() ([]psRow, error) {
	if !DockerOK() {
		return nil, fmt.Errorf("docker not found in PATH")
	}
	ctx, cancel := probeContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "ps", "-a",
		"--format", `{{.Names}}\t{{.State}}\t{{.Status}}\t{{.Label "tailarr.service"}}`)
	raw, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("docker ps timed out after %s", probeTimeout)
		}
		return nil, fmt.Errorf("docker ps: %w", err)
	}
	return parsePS(string(raw)), nil
}

func parsePS(raw string) []psRow {
	var rows []psRow
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		r := psRow{name: parts[0], state: strings.ToLower(parts[1])}
		if len(parts) >= 3 {
			r.status = parts[2]
		}
		if len(parts) >= 4 {
			r.label = parts[3]
		}
		rows = append(rows, r)
	}
	return rows
}

// serviceContainers returns the containers of service, sorted by name, and
// their worst health. A service without containers, or whose containers all
// exited (compose stop leaves them so), is stopped: exited only counts as a
// failure while other containers of the service still run.
func serviceContainers(rows []psRow, service string) ([]Container, Health) {
	var out []Container
	worst := HealthStopped
	allDown := true
	for _, r := range rows {
		if !containerMatchesService(r.name, r.label, service) {
			continue
		}
		h := classifyHealth(r.state, strings.ToLower(r.status))
		if len(out) == 0 || healthRank(h) > healthRank(worst) {
			worst = h
		}
		if r.state != "exited" && r.state != "created" {
			allDown = false
		}
		out = append(out, Container{Name: r.name, State: r.state, Status: r.status, Health: h})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if allDown {
		worst = HealthStopped
	}
	return out, worst
}

// scaleTailServiceFromContainer returns the service a ScaleTail container
// name (app-<service> or tailscale-<service>) belongs to, or "".
func scaleTailServiceFromContainer(container string) string {
	switch {
	case strings.HasPrefix(container, "app-"):
		return strings.TrimPrefix(container, "app-")
	case strings.HasPrefix(container, "tailscale-"):
		return strings.TrimPrefix(container, "tailscale-")
	default:
		return ""
	}
}

// containerMatchesService reports whether a container belongs to service,
// by its tailarr.service label or an exact app-/tailscale- name. Docker's
// --filter name= matches substrings, so "web" would include "web-ui".
func containerMatchesService(container, label, service string) bool {
	if label != "" && label == service {
		return true
	}
	name := scaleTailServiceFromContainer(container)
	return name != "" && name == service
}

func classifyHealth(state, status string) Health {
	switch state {
	case "running":
		switch {
		case strings.Contains(status, "(healthy)"):
			return HealthHealthy
		case strings.Contains(status, "(health: starting)"), strings.Contains(status, "(starting)"):
			return HealthStarting
		case strings.Contains(status, "(unhealthy)"):
			return HealthUnhealthy
		default:
			return HealthRunning
		}
	case "exited", "dead", "created", "paused", "restarting", "removing":
		return HealthExited
	default:
		return HealthUnknown
	}
}

// healthRank orders health from best (0) to worst.
func healthRank(h Health) int {
	switch h {
	case HealthHealthy:
		return 0
	case HealthRunning:
		return 1
	case HealthStarting:
		return 2
	case HealthExited, HealthStopped:
		return 4
	case HealthUnhealthy:
		return 5
	default:
		return 3
	}
}
