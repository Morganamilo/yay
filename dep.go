package main

import (
	"fmt"
	"strings"

	alpm "github.com/jguer/go-alpm"
	rpc "github.com/mikkeloscar/aur"
)

func splitDep(dep string) (string, string, string) {
	mod := ""

	split := strings.FieldsFunc(dep, func(c rune) bool {
		match := c == '>' || c == '<' || c == '='

		if match {
			mod += string(c)
		}

		return match
	})

	if len(split) == 1 {
		return split[0], "", ""
	}

	return split[0], mod, split[1]
}

func pkgSatisfies(name, version, dep string) bool {
	depName, depMod, depVersion := splitDep(dep)

	if depName != name {
		return false
	}

	return verSatisfies(version, depMod, depVersion)
}

func provideSatisfies(provide, dep string) bool {
	depName, depMod, depVersion := splitDep(dep)
	provideName, provideMod, provideVersion := splitDep(provide)

	if provideName != depName {
		return false
	}

	// Unversioned provieds can not satisfy a versioned dep
	if provideMod == "" && depMod != "" {
		return false
	}

	return verSatisfies(provideVersion, depMod, depVersion)
}

func verSatisfies(ver1, mod, ver2 string) bool {
	switch mod {
	case "=":
		return alpm.VerCmp(ver1, ver2) == 0
	case "<":
		return alpm.VerCmp(ver1, ver2) < 0
	case "<=":
		return alpm.VerCmp(ver1, ver2) <= 0
	case ">":
		return alpm.VerCmp(ver1, ver2) > 0
	case ">=":
		return alpm.VerCmp(ver1, ver2) >= 0
	}

	return true
}

func satisfiesAur(dep string, pkg *rpc.Pkg) bool {
	if pkgSatisfies(pkg.Name, pkg.Version, dep) {
			return true
	}

	for _, provide := range pkg.Provides {
		if provideSatisfies(provide, dep) {
			return true
		}
	}

	return false
}

func satisfiesRepo(dep string, pkg *alpm.Package) bool {
	if pkgSatisfies(pkg.Name(), pkg.Version(), dep) {
		return true
	}

	if pkg.Provides().ForEach(func(provide alpm.Depend) error {
		if provideSatisfies(provide.String(), dep) {
			return fmt.Errorf("")
		}

		return nil
	}) != nil {
		return true
	}

	return false
}
