package main

import (
	"fmt"
	"strings"

	alpm "github.com/jguer/go-alpm"
	rpc "github.com/mikkeloscar/aur"
//	gopkg "github.com/mikkeloscar/gopkgbuild"
)

type source int

const (
	repository source = iota
	build
)

type remotePackage interface {
	Source() source
	Name() string
	Version() string
}

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

type dependencyTree struct {
	targets []string
	Queue []string
	Repo     []*alpm.Package
	Aur      []*rpc.Pkg
	Groups    []string
	LocalDB *alpm.Db
	RemoteDB alpm.DbList
	Warnings *aurWarnings
}

func makeDependencyTree() (*dependencyTree, error) {
	localDb, err := alpmHandle.LocalDb()
	if err != nil {
		return nil, err
	}
	syncDb, err := alpmHandle.SyncDbs()
	if err != nil {
		return nil, err
	}

	dt := &dependencyTree {
		make([]string, 0),
		make([]string, 0),
		make([]*alpm.Package, 0),
		make([]*rpc.Pkg, 0),
		make([]string, 0),
		localDb,
		syncDb,
		&aurWarnings{},
	}

	return dt, nil
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
	provideName, provideMod, provideVersion := splitDep(dep)

	if provideName != depName {
		return false
	}

	// Unversioned provieds can not satisfy a versioned dep
	if provideMod == "" && depMod != "" {
		return false
	}

	return verSatisfies(provideVersion, depMod, depVersion)
}


func (dt *dependencyTree) resolveNeeded(pkgs string) (*dependencyTree, error) {
return nil, nil
}

func (dt *dependencyTree) resolveTargets(targets string) error {
	aurPkgs := make(stringSet)

	for _, pkg := range targets {
		db, name := splitDbFromName(pkg)
		var foundPkg *alpm.Package
		var singleDb *alpm.Db

		if db == "aur" {
			aurPkgs.set(name)
			continue
		}

		// Check the repos for a matching dep
		if db != "" {
			singleDb, err = alpmHandle.SyncDbByName(db)
			if err != nil {
				return dt, err
			}
			foundPkg, err = singleDb.PkgCache().FindSatisfier(name)
		} else {
			foundPkg, err = syncDb.FindSatisfier(name)
		}

		if err == nil {
			//repoTreeRecursive(foundPkg, dt, localDb, syncDb)
			continue
		} else {
			//would be better to check the groups from singleDb if
			//the user specified a db but theres no easy way to do
			//it without making alpm_lists so dont bother for now
			//db/group is probably a rare use case
			_, err := syncDb.PkgCachebyGroup(name)

			if err == nil {
				dt.Groups = append(dt.Groups, pkg)
				continue
			}
		}

		if db == "" {
			dt.ToProcess.set(name)
		} else {
			dt.Missing.set(pkg)
		}

}

func (dt *dependencyTree) getDependencyTree(pkgs []string) (*dependencyTree, error) {
	dt, err := makeDependencyTree()
	if err != nil {
		return nil, err
	}

	dt.ResolveTargets()

	return nil, nil
}

func (dt *dependencyTree) queueNeeded(pkg string) {
	if !dt.hasSatisfier(pkg) {
		dt.Queue = append(dt.Queue, pkg)
	}
}

func (dt *dependencyTree) findSatisfierAur(dep string) *rpc.Pkg {
	for _, pkg := range dt.Aur {
		if pkgSatisfies(pkg.Name, pkg.Version, dep) {
			return pkg
		}

		for _, provide := range pkg.Provides {
			if provideSatisfies(provide, dep) {
				return pkg
			}
		}
	}

	return nil
}

func (dt *dependencyTree) findSatisfierRepo(dep string) *alpm.Package {
	for _, pkg := range dt.Repo {
		if pkgSatisfies(pkg.Name(), pkg.Version(), dep) {
			return pkg
		}

		if pkg.Provides().ForEach(func(provide alpm.Depend) error  {
			if provideSatisfies(provide.String(), dep) {
				return fmt.Errorf("")
			}

			return nil
		}) != nil {
			return pkg
		}
	}

	return nil
}

func (dt *dependencyTree) hasSatisfier(dep string) bool {
	return dt.findSatisfierRepo(dep) != nil || dt.findSatisfierAur(dep) != nil
}
