package main

import (
	alpm "github.com/jguer/go-alpm"
)

func ParseDepend(dep string) alpm.Depend {
	name, mod, version := splitDep(dep)

	return alpm.Depend{
		Name: name,
		Version: version,
		Mod: ParseDepMod(mod),
	}
}

func ParseDepends(deps []string) []alpm.Depend {
	d := make([]alpm.Depend, len(deps))

	for _, dep := range deps {
		d = append(d, ParseDepend(dep))
	}

	return d
}

func ParseDepMod(mod string) alpm.DepMod {
	switch mod {
	case "=":
		return alpm.DepModEq
	case ">=":
		return alpm.DepModGE
	case "<=":
		return alpm.DepModLE
	case ">":
		return alpm.DepModGT
	case "<":
		return alpm.DepModLT
	}

	return alpm.DepModAny
}
