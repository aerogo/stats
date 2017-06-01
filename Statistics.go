package stats

import (
	"encoding/json"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aerogo/aero"
	sigar "github.com/cloudfoundry/gosigar"
	humanize "github.com/dustin/go-humanize"
	"github.com/julienschmidt/httprouter"
)

// Statistics for a given app.
type Statistics struct {
	app    *aero.Application
	routes map[string]*RouteStatistics
}

// Route statistics
type Route struct {
	Route        string
	Requests     uint64
	ResponseTime uint64
}

// NewStatistics creates a new statistics instance.
func NewStatistics(app *aero.Application) *Statistics {
	stats := new(Statistics)
	stats.app = app
	stats.routes = make(map[string]*RouteStatistics)

	return stats
}

// show ...
func (stats *Statistics) show(path string) {
	// Statistics route
	stats.app.router.GET(path, func(response http.ResponseWriter, request *http.Request, params httprouter.Params) {
		var memStats runtime.MemStats
		runtime.ReadMemStats(&memStats)

		avg := sigar.LoadAverage{}
		uptime := sigar.Uptime{}

		avg.Get()
		uptime.Get()

		mem := sigar.Mem{}
		mem.Get()

		type AppMemoryStats struct {
			Allocated   string
			GCThreshold string
			Objects     uint64
		}

		type SystemMemoryStats struct {
			Total string
			Free  string
			Cache string
		}

		type AppStats struct {
			Go       string
			Uptime   string
			Requests uint64
			Memory   AppMemoryStats
			Config   *aero.Configuration
		}

		type SystemStats struct {
			Uptime      string
			CPUs        int
			LoadAverage sigar.LoadAverage
			Memory      SystemMemoryStats
		}

		type RouteSummary struct {
			Slow    []*Route
			Popular []*Route
		}

		routeSummary := RouteSummary{}

		for path, stats := range stats.routes {
			route := &Route{
				Route:        path,
				Requests:     atomic.LoadUint64(&stats.requestCount),
				ResponseTime: uint64(stats.AverageResponseTime()),
			}

			if route.ResponseTime >= 10 {
				routeSummary.Slow = append(routeSummary.Slow, route)
			}

			if route.Requests >= 1 {
				routeSummary.Popular = append(routeSummary.Popular, route)
			}
		}

		sort.Slice(routeSummary.Slow, func(i, j int) bool {
			return routeSummary.Slow[i].ResponseTime > routeSummary.Slow[j].ResponseTime
		})

		sort.Slice(routeSummary.Popular, func(i, j int) bool {
			return routeSummary.Popular[i].Requests > routeSummary.Popular[j].Requests
		})

		stats := struct {
			System SystemStats
			App    AppStats
			Routes RouteSummary
		}{
			System: SystemStats{
				Uptime:      strings.TrimSpace(uptime.Format()),
				CPUs:        runtime.NumCPU(),
				LoadAverage: avg,
				Memory: SystemMemoryStats{
					Total: humanize.Bytes(mem.Total),
					Free:  humanize.Bytes(mem.Free),
					Cache: humanize.Bytes(mem.Used - mem.ActualUsed),
				},
			},
			App: AppStats{
				Go:       strings.Replace(runtime.Version(), "go", "", 1),
				Uptime:   strings.TrimSpace(humanize.RelTime(stats.app.StartTime(), time.Now(), "", "")),
				Requests: stats.RequestCount(),
				Memory: AppMemoryStats{
					Allocated:   humanize.Bytes(memStats.HeapAlloc),
					GCThreshold: humanize.Bytes(memStats.NextGC),
					Objects:     memStats.HeapObjects,
				},
				Config: stats.app.Config,
			},
			Routes: routeSummary,
		}

		// numCPU :=
		// var b bytes.Buffer
		// b.WriteString("Server statistics:\n")

		// b.WriteString("\nGo version: ")
		// b.WriteString(runtime.Version())

		// b.WriteString("\nCPUs: ")
		// b.WriteString(strconv.Itoa(numCPU))

		response.Header().Set("Content-Type", "application/json")
		bytes, err := json.Marshal(stats)
		if err != nil {
			response.Write(aero.StringToBytesUnsafe("Error serializing to JSON"))
			return
		}
		response.Write(bytes)
	})
}

// RequestCount calculates the total number of requests made to the application.
func (stats *Statistics) RequestCount() uint64 {
	total := uint64(0)

	for _, route := range stats.routes {
		total += atomic.LoadUint64(&route.requestCount)
	}

	return total
}
