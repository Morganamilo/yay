package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	alpm "github.com/jguer/go-alpm"
	rpc "github.com/mikkeloscar/aur"
	//gopkg "github.com/mikkeloscar/gopkgbuild"
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
	Repo     []*alpm.Package
	Aur      map[string]*rpc.Pkg
	AurCache map[string]*rpc.Pkg
	Groups   []string
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
		make([]*alpm.Package, 0),
		make(map[string]*rpc.Pkg),
		make(map[string]*rpc.Pkg),
		make([]string, 0),
		localDb,
		syncDb,
		&aurWarnings{},
	}

	return dt, nil
}

func (dt *dependencyTree) String() string {
	str := ""
	str += "\nRepo (" + strconv.Itoa(len(dt.Repo)) + ") :"
	for _, pkg := range dt.Repo {
		str += " " + pkg.Name()
	}

	str += "\nAur (" + strconv.Itoa(len(dt.Aur)) + ") :"
	for pkg := range dt.Aur {
		str += " " + pkg
	}

	str += "\nAur Cache (" + strconv.Itoa(len(dt.AurCache)) + ") :"
	for pkg := range dt.AurCache {
		str += " " + pkg
	}

	str += "\nGroups (" + strconv.Itoa(len(dt.Groups)) + ") :"
	for _, pkg := range dt.Groups {
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

// Includes db/ prefixes and group installs
func (dt *dependencyTree) ResolveTargets() error {
	// RPC requests are slow
	// Combine as many AUR package requests as possible into a single RPC
	// call
	aurTargets := make(stringSet)
	var err error
	//repo := make([]*alpm.Package, 0)

	for _, target := range dt.Targets {

		// skip targets already satisfied
		// even if the user enters db/pkg and aur/pkg the latter will
		// still get skiped even if it's from a different database to
		// the one specified
		// this is how pacman behaves
		if dt.hasSatisfier(target.DepString()) {
			fmt.Println("Skipping target", target)
			continue
		}

		var foundPkg *alpm.Package
		var singleDb *alpm.Db

		// aur/ prefix means we only check the aur
		if target.Db == "aur" {
			aurTargets.set(target.DepString())
			continue
		}

		// if theres a different priefix only look in that repo
		if target.Db != "" {
			singleDb, err = alpmHandle.SyncDbByName(target.Db)
			if err != nil {
				return err
			}
			foundPkg, err = singleDb.PkgCache().FindSatisfier(target.DepString())
			//otherwise find it in any repo
		} else {
			foundPkg, err = dt.SyncDb.FindSatisfier(target.DepString())
		}

		if err == nil {
			//dt.Repo = append(dt.Repo, foundPkg)
			dt.ResolveRepoDependency(foundPkg)
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
			aurTargets.set(target.DepString())
		}
	}

	if len(aurTargets) > 0 {
		err = dt.resolveAURPackages(aurTargets)
	}

	return nil
}

// Pseudo provides finder.
// Try to find provides by performing a search of the package name
// This effectively performs -Ss on each package
// then runs -Si on each result to cache the information.
//
// For example if you were to -S yay then yay -Ss would give:
// yay-git yay-bin yay realyog pacui pacui-git ruby-yard
// These packages will all be added to the cache incase they are needed later
// Ofcouse only the first three packages provide yay, the rest are just false
// positives.
//
// This method increasing dependency resolving time expenentionally
func (dt *dependencyTree) superFetch(pkgs stringSet) error {
	var mux sync.Mutex
	var wg sync.WaitGroup
	var err error

	doSearch := func(pkg string) {
		defer wg.Done()

		results, localerr := rpc.SearchByNameDesc(pkg)
		if localerr != nil {
			err = localerr
			return
		}

		for _, result := range results {
			mux.Lock()
			if _, ok := dt.AurCache[result.Name]; !ok {
				pkgs.set(result.Name)
			}
			mux.Unlock()
		}
	}

	for pkg := range pkgs {
		wg.Add(1)
		go doSearch(pkg)
	}

	wg.Wait()

	return nil
}

func (dt *dependencyTree) cacheAURPackages(_pkgs stringSet) error {
	pkgs := _pkgs.copy()
	query := make([]string, 0)
	
	for pkg := range pkgs {
		if _, ok := dt.AurCache[pkg]; ok {
			pkgs.remove(pkg)
		}
	}

	//TODO: config option, maybe --deepfetch but aur man uses that flag for
	//something else already which might be confusing
	//maybe --provides
	if true {
		err := dt.superFetch(pkgs)
		if err != nil {
			return err
		}
	}

	for pkg := range pkgs {
		if _, ok := dt.AurCache[pkg]; !ok {
			name, _, _ := splitDep(pkg)
			query = append(query, name)
		}
	}

	if len(pkgs) == 0 {
		return nil
	}

	info, err := aurInfo(query, dt.Warnings)
	if err != nil {
		return err
	}

	for _, pkg := range info {
		// Dump everything in cache just in case we need it later
		dt.AurCache[pkg.Name] = pkg
	}

	return nil
}

func (dt *dependencyTree) resolveAURPackages(pkgs stringSet) error {
	newPackages := make(stringSet)
	newAURPackages := make(stringSet)

	err := dt.cacheAURPackages(pkgs)
	if err != nil {
		return err
	}

	if len(pkgs) == 0 {
		return nil
	}

	for name := range pkgs {
		fmt.Println(name)
		_, ok := dt.Aur[name]
		if ok {
			continue
		}

		pkg := dt.findSatisfierAurCache(name)
		if pkg == nil {
			continue
		}

		dt.Aur[pkg.Name] = pkg

		for _, deps := range [3][]string{pkg.Depends, pkg.MakeDepends, pkg.CheckDepends} {
			for _, dep := range deps {
				newPackages.set(dep)
			}
		}
	}

	for dep := range newPackages {
		if dt.hasSatisfier(dep) {
			continue
		}

		//has satisfier installed: skip
		_, isInstalled := dt.LocalDb.PkgCache().FindSatisfier(dep)
		if isInstalled == nil {
			continue
		}

		//has satisfier in repo: fetch it
		repoPkg, inRepos := dt.SyncDb.FindSatisfier(dep)
		if inRepos == nil {
			dt.ResolveRepoDependency(repoPkg)
			continue
		}

		//assume it's in the aur
		//ditch the versioning because the RPC cant handle it
		newAURPackages.set(dep)

	}

	err = dt.resolveAURPackages(newAURPackages)

	return err
}

func (dt *dependencyTree) ResolveRepoDependency(pkg *alpm.Package) {
	dt.Repo = append(dt.Repo, pkg)

	pkg.Depends().ForEach(func(dep alpm.Depend) (err error) {
		//have satisfier in dep tree: skip
		if dt.hasSatisfier(dep.String()) {
			return
		}

		//has satisfier installed: skip
		_, isInstalled := dt.LocalDb.PkgCache().FindSatisfier(dep.String())
		if isInstalled == nil {
			return
		}

		//has satisfier in repo: fetch it
		repoPkg, inRepos := dt.SyncDb.FindSatisfier(dep.String())
		if inRepos != nil {
			return
		}

		dt.ResolveRepoDependency(repoPkg)

		return nil
	})

}

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

func (dt *dependencyTree) findSatisfierAurCache(dep string) *rpc.Pkg {
	for _, pkg := range dt.AurCache {
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
