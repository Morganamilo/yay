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

type target struct {
	Db      string
	Name    string
	Mod     string
	Version string
}

func toTarget(pkg string) target {
	db, dep := splitDbFromName(pkg)
	name, mod, version := splitDep(dep)

	return target{
		db,
		name,
		mod,
		version,
	}
}

func (t target) DepString() string {
	return t.Name + t.Mod + t.Version
}

func (t target) String() string {
	if t.Db != "" {
		return t.Db + "/" + t.DepString()
	}

	return t.DepString()
}

type dependencyTree struct {
	Targets  []target
	Queue    []string
	Repo     []*alpm.Package
	Aur      []*rpc.Pkg
	AurCache []*rpc.Pkg
	Groups   []string
	Missing  []string
	LocalDb  *alpm.Db
	SyncDb   alpm.DbList
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

	dt := &dependencyTree{
		make([]target, 0),
		make([]string, 0),
		make([]*alpm.Package, 0),
		make([]*rpc.Pkg, 0),
		make([]*rpc.Pkg, 0),
		make([]string, 0),
		make([]string, 0),
		localDb,
		syncDb,
		&aurWarnings{},
	}

	return dt, nil
}

func (dt *dependencyTree) String() string {
	str := ""
	str += "\nRepo:"
	for _, pkg := range dt.Repo {
		str += " " + pkg.Name()
	}

	str += "\nAur:"
	for _, pkg := range dt.Aur {
		str += " " + pkg.Name
	}

	str += "\nGroups:"
	for _, pkg := range dt.Groups {
		str += " " + pkg
	}

	str += "\nMissing:"
	for _, pkg := range dt.Missing {
		str += " " + pkg
	}

	return str
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

// Includes db/ prefixes and group installs
func (dt *dependencyTree) ResolveTargets() error {
	aurTargets := make([]target, 0)

	for _, target := range dt.Targets {

		// skip targets already satisfied
		// even if the user enters db/pkg and aur/pkg the latter will
		// still get skipt even if it's from a different database to
		// the one specified
		// this is how pacman behaves
		if dt.hasSatisfier(target.DepString()) {
			fmt.Println("Skipping target", target)
			continue
		}

		var foundPkg *alpm.Package
		var singleDb *alpm.Db
		var err error

		// aur/ prefix means we only check the aur
		if target.Db == "aur" {
			aurTargets = append(aurTargets, target)
			continue
		}

		// if theres a different prfix only look in that repo
		if target.Db != "" {
			singleDb, err = alpmHandle.SyncDbByName(target.Db)
			if err != nil {
				return err
			}
			foundPkg, err = singleDb.PkgCache().FindSatisfier(target.Name)
			//otherwise find it in any repo
		} else {
			foundPkg, err = dt.SyncDb.FindSatisfier(target.Name)
		}

		if err == nil {
			dt.Repo = append(dt.Repo, foundPkg)
			//repoTreeRecursive(foundPkg, dt, localDb, syncDb)
			continue
		} else {
			//check for groups
			//currently we dont resolve the packages in a group
			//only check if the group exists
			//would be better to check the groups from singleDb if
			//the user specified a db but theres no easy way to do
			//it without making alpm_lists so dont bother for now
			//db/group is probably a rare use case
			_, err := dt.SyncDb.PkgCachebyGroup(target.Name)

			if err == nil {
				dt.Groups = append(dt.Groups, target.String())
				continue
			}
		}

		//if there was no db prefix check the aur
		if target.Db == "" {
			aurTargets = append(aurTargets, target)
		}
	}

	if len(aurTargets) == 0 {
		return nil
	}

	names := make(stringSet)
	for _, target := range aurTargets {
		names.set(target.Name)
	}

	info, err := aurInfo(names.toSlice(), dt.Warnings)
	if err != nil {
		return err
	}

	// Dump everything in cache just in case we need it later
	for _, pkg := range info {
		dt.AurCache = append(dt.AurCache, pkg)
	}

outer:
	for _, target := range dt.Targets {
		//first pass just look for a matching name
		for _, pkg := range dt.AurCache {
			if pkgSatisfies(pkg.Name, pkg.Version, target.DepString()) {
				dt.Aur = append(dt.Aur, pkg)
				continue outer
			}
		}

		satisfiers := make([]*rpc.Pkg, 0)

		//look for provides then
		for _, pkg := range dt.AurCache {
			for _, provide := range pkg.Provides {
				if provideSatisfies(provide, target.DepString()) {
					satisfiers = append(satisfiers, pkg)
					continue outer
				}
			}
		}

		// didnt find a satisfier, how sad
		if len(satisfiers) == 0 {
			continue
		}

		//TODO add a menu
		dt.Aur = append(dt.Aur, satisfiers[0])
	}

	return nil
}

func (dt *dependencyTree) ResolveRepoDependencies(pkgs string) error {
	return nil
}

// Resolves the targets specified by the user

func (dt *dependencyTree) queryAUR(pkgs []string) error {
	_, err := aurInfo(pkgs, dt.Warnings)
	if err != nil {
		return err
	}

	return nil

}

func getDependencyTree() (*dependencyTree, error) {
	dt, err := makeDependencyTree()
	if err != nil {
		return nil, err
	}

	return dt, err
}

func (dt *dependencyTree) ParseTargets(pkgs []string) {
	for _, pkg := range pkgs {
		target := toTarget(pkg)
		dt.Targets = append(dt.Targets, target)
	}
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

		if pkg.Provides().ForEach(func(provide alpm.Depend) error {
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

func (dt *dependencyTree) hasPackage(name string) bool {
	for _, pkg := range dt.Repo {
		if pkg.Name() == name {
			return true
		}
	}

	for _, pkg := range dt.Aur {
		if pkg.Name == name {
			return true
		}
	}

	for _, pkg := range dt.Groups {
		if pkg == name {
			return true
		}
	}

	return false
}
