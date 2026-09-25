package tui

import "github.com/fishman/notmutt/lib/tui/table"

type tableColumn struct {
	heading  string
	width    table.Col
	dropRank int
}

var resourceColumns = [...]tableColumn{
	{"Name", table.Col{Floor: 12, Cap: 24}, 10},
	{"Type", table.Col{Floor: 12, Cap: 18}, 6},
	{"Format", table.Col{Floor: 6, Cap: 8}, 5},
	{"Source", table.Col{Floor: 14, Cap: 24}, 4},
	{"Enabled", table.Col{Floor: 7, Cap: 8}, 9},
	{"Validated", table.Col{Floor: 9, Cap: 10}, 7},
	{"Next", table.Col{Floor: 12, Cap: 18}, 3},
}

var subscriptionColumns = [...]tableColumn{
	{"Name", table.Col{Floor: 12, Cap: 24}, 10},
	{"Source", table.Col{Floor: 14, Cap: 24}, 6},
	{"State", table.Col{Floor: 9, Cap: 16}, 9},
	{"Last check", table.Col{Floor: 16, Cap: 18}, 5},
	{"Next", table.Col{Floor: 16, Cap: 18}, 4},
	{"Usage", table.Col{Floor: 16, Cap: 28}, 3},
}

var filterColumns = [...]tableColumn{
	{"Name", table.Col{Floor: 12, Cap: 24}, 10},
	{"Format", table.Col{Floor: 6, Cap: 8}, 5},
	{"Target", table.Col{Floor: 10, Cap: 18}, 6},
	{"Source", table.Col{Floor: 14, Cap: 24}, 4},
	{"Enabled", table.Col{Floor: 7, Cap: 8}, 9},
	{"Validated", table.Col{Floor: 9, Cap: 10}, 7},
	{"Next", table.Col{Floor: 12, Cap: 18}, 3},
}

var proxyColumns = [...]tableColumn{
	{"Group", table.Col{Floor: 12, Cap: 20}, 7},
	{"Proxy", table.Col{Floor: 12, Cap: 24}, 10},
	{"Selected", table.Col{Floor: 8, Cap: 10}, 6},
	{"Latency", table.Col{Floor: 8, Cap: 12}, 5},
	{"Outcome", table.Col{Floor: 8, Cap: 12}, 3},
	{"Automation", table.Col{Floor: 11, Cap: 12}, 4},
}

var settingsColumns = [...]tableColumn{
	{"Setting", table.Col{Floor: 12, Cap: 28}, 10},
	{"Value", table.Col{Floor: 12, Cap: 64}, 7},
	{"Action", table.Col{Floor: 8, Cap: 16}, 3},
}

var overviewColumns = [...]tableColumn{
	{"Name", table.Col{Floor: 12, Cap: 24}, 10},
	{"Details", table.Col{Floor: 16, Cap: 80}, 7},
}

func tableColumns(tab Tab) []tableColumn {
	switch tab {
	case TabProxies:
		return proxyColumns[:]
	case TabSubscriptions:
		return subscriptionColumns[:]
	case TabFilters:
		return filterColumns[:]
	case TabResources:
		return resourceColumns[:]
	case TabSettings:
		return settingsColumns[:]
	default:
		return overviewColumns[:]
	}
}

func fitTable(columns []tableColumn, width int) ([]int, table.Layout, []int) {
	indexes := make([]int, len(columns))
	for i := range indexes {
		indexes[i] = i
	}
	for len(indexes) > 1 {
		used := (len(indexes) - 1) * 2
		for _, index := range indexes {
			used += columns[index].width.Floor
		}
		if used <= width {
			break
		}
		drop := 0
		for i := 1; i < len(indexes); i++ {
			if columns[indexes[i]].dropRank < columns[indexes[drop]].dropRank {
				drop = i
			}
		}
		indexes = append(indexes[:drop], indexes[drop+1:]...)
	}
	layout := table.Layout{Cols: make([]table.Col, len(indexes)), Sep: "  "}
	for i, index := range indexes {
		layout.Cols[i] = columns[index].width
	}
	if len(indexes) == 1 && layout.Cols[0].Floor > width {
		layout.Cols[0] = table.Col{Floor: width, Cap: width}
	}
	return indexes, layout, layout.Sizes(width, false)
}
