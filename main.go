package main

import (
//    "compress/gzip"
    "crypto/md5"
    "crypto/sha1"
    "crypto/sha256"
    "crypto/sha512"
    "flag"
    "fmt"
    "github.com/ulikunitz/xz"
    "hash"
    "io"
    "io/ioutil"
    "os"
    "os/exec"
    "path/filepath"
    "regexp"
    "sort"
    "strings"
    "time"
)

var (
    COMPONENTS        []string
    supportedArches   = []string{"all", "arm", "aarch64"}
    encounteredArches = map[string]bool{}
    hashes            = []string{"md5", "sha1", "sha256", "sha512"}
    inputPath         string
    outputPath        string
    distribution      string
    defaultComponent  string
    useHardLinks      bool
    signRepo          bool
    distributionPath  string
)

func init() {
    flag.StringVar(&inputPath, "input", "", "folder where .deb files are located")
    flag.StringVar(&outputPath, "output", "", "folder with repository tree")
    flag.StringVar(&distribution, "distribution", "termux", "name of distribution folder")
    flag.StringVar(&defaultComponent, "component", "extras", "name of component folder")
    flag.BoolVar(&useHardLinks, "use-hard-links", false, "use hard links instead of copying deb files")
    flag.BoolVar(&signRepo, "sign", false, "sign repo with GPG key")
    flag.Usage = func() {
        fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
        flag.PrintDefaults()
    }
    versionFlag := flag.Bool("version", false, "Display version information")
    flag.Parse()
    if *versionFlag {
        fmt.Println("termux-apt-builder v1.0\nby PhateValleyman\nJonas.Ned@outlook.com")
        os.Exit(0)
    }
}

func getPackageName(filename string) string {
    return strings.Split(filename, "_")[0]
}

func runShellCommand(cmd string) (string, error) {
    out, err := exec.Command("sh", "-c", cmd).Output()
    if err != nil {
        return "", err
    }
    return string(out), nil
}

func controlFileContents(debfile string) string {
    fileList, err := runShellCommand(fmt.Sprintf("ar t %s", debfile))
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error listing contents of '%s'\n", debfile)
        os.Exit(1)
    }

    var controlFilename, tarArgs string
    if strings.Contains(fileList, "control.tar.gz") {
        controlFilename = "control.tar.gz"
        tarArgs = "-z"
    } else if strings.Contains(fileList, "control.tar.xz") {
        controlFilename = "control.tar.xz"
        tarArgs = "-J"
    } else {
        fmt.Fprintf(os.Stderr, "Failed to find control file in '%s'\n", debfile)
        os.Exit(1)
    }

    cmd := fmt.Sprintf("ar p %s %s | tar -O %s -xf - ./control", debfile, controlFilename, tarArgs)
    contents, err := runShellCommand(cmd)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error extracting control file from '%s'\n", debfile)
        os.Exit(1)
    }

    return contents
}

func listPackageFiles(debfile string) []string {
    allContent, err := runShellCommand(fmt.Sprintf("ar p %s data.tar.xz | tar -tJ", debfile))
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error listing package files for '%s'\n", debfile)
        os.Exit(1)
    }
    var files []string
    for _, file := range strings.Split(allContent, "\n") {
        if len(file) > 0 && file[len(file)-1] != '/' {
            files = append(files, strings.TrimPrefix(file, "./"))
        }
    }
    return files
}

func addDeb(debToAddPath, component string, useHardLinks bool) {
    debToAddControlFile := controlFileContents(debToAddPath)
    debToAddPkgName := regexp.MustCompile(`Package: (.*)`).FindStringSubmatch(debToAddControlFile)[1]
    debArch := regexp.MustCompile(`Architecture: (.*)`).FindStringSubmatch(debToAddControlFile)[1]

    if !contains(supportedArches, debArch) {
        fmt.Fprintf(os.Stderr, "Unsupported arch '%s' in '%s'\n", debArch, filepath.Base(debToAddPath))
        os.Exit(1)
    }
    encounteredArches[debArch] = true

    archDirPath := filepath.Join(distributionPath, component, "binary-"+debArch)

    if _, err := os.Stat(archDirPath); os.IsNotExist(err) {
        os.MkdirAll(archDirPath, 0755)
    }

    fmt.Println("Adding deb file:", filepath.Base(debToAddPath))
    destDebDirPath := filepath.Join(distributionPath, component, "binary-"+debArch)
    if _, err := os.Stat(destDebDirPath); os.IsNotExist(err) {
        os.MkdirAll(destDebDirPath, 0755)
    }
    destinationDebFile := filepath.Join(destDebDirPath, filepath.Base(debToAddPath))

    if useHardLinks {
        os.Link(debToAddPath, destinationDebFile)
    } else {
        if err := copyFile(debToAddPath, destinationDebFile); err != nil {
            fmt.Fprintf(os.Stderr, "Error copying file '%s'\n", debToAddPath)
            os.Exit(1)
        }
    }

    contentsFile, err := os.OpenFile(filepath.Join(distributionPath, component, "Contents-"+debArch), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error opening contents file for '%s'\n", debArch)
        os.Exit(1)
    }
    defer contentsFile.Close()

    for _, f := range listPackageFiles(destinationDebFile) {
        fmt.Fprintf(contentsFile, "%-80s %s\n", f, debToAddPkgName)
    }
}

func copyFile(src, dst string) error {
    inFile, err := os.Open(src)
    if err != nil {
        return err
    }
    defer inFile.Close()

    outFile, err := os.Create(dst)
    if err != nil {
        return err
    }
    defer outFile.Close()

    _, err = io.Copy(outFile, inFile)
    if err != nil {
        return err
    }
    return outFile.Sync()
}

func contains(slice []string, item string) bool {
    for _, v := range slice {
        if v == item {
            return true
        }
    }
    return false
}

func main() {
    flag.Parse()

    if inputPath == "" || outputPath == "" {
        flag.Usage()
        os.Exit(1)
    }

    distributionPath = filepath.Join(outputPath, "dists", distribution)

    if _, err := os.Stat(inputPath); os.IsNotExist(err) {
        fmt.Fprintf(os.Stderr, "'%s' does not exist\n", inputPath)
        os.Exit(1)
    }

    debsInPath, err := filepath.Glob(filepath.Join(inputPath, "*.deb"))
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error globbing .deb files in '%s'\n", inputPath)
        os.Exit(1)
    }
    debsInSubPath, err := filepath.Glob(filepath.Join(inputPath, "*/*.deb"))
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error globbing .deb files in subdirectories of '%s'\n", inputPath)
        os.Exit(1)
    }
    debsInPath = append(debsInPath, debsInSubPath...)

    if len(debsInPath) == 0 {
        fmt.Fprintf(os.Stderr, "No .deb file found in '%s'\n", inputPath)
        os.Exit(1)
    }

    for _, debPath := range debsInPath {
        component := filepath.Dir(strings.TrimPrefix(debPath, inputPath))
        if component == "." {
            component = defaultComponent
        }
        if !contains(COMPONENTS, component) {
            COMPONENTS = append(COMPONENTS, component)
            if _, err := os.Stat(filepath.Join(distributionPath, component)); err == nil {
                os.RemoveAll(filepath.Join(distributionPath, component))
            }
        }
        addDeb(debPath, component, useHardLinks)
    }

    releaseFilePath := filepath.Join(distributionPath, "Release")
    releaseFile, err := os.Create(releaseFilePath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error creating Release file\n")
        os.Exit(1)
    }
    defer releaseFile.Close()

    fmt.Fprintln(releaseFile, "Codename: termux")
    fmt.Fprintln(releaseFile, "Version: 1")
    fmt.Fprintln(releaseFile, "Architectures:", strings.Join(mapKeys(encounteredArches), " "))
    fmt.Fprintln(releaseFile, "Description:", distribution, "repository")
    fmt.Fprintln(releaseFile, "Suite:", distribution)
    fmt.Fprintln(releaseFile, "Date:", time.Now().UTC().Format(time.RFC1123))

    for _, component := range COMPONENTS {
        for _, archDirPath := range glob(filepath.Join(distributionPath, component, "binary-*")) {
            arch := strings.Split(filepath.Base(archDirPath), "-")[1]
            fmt.Println("Creating package file for", component, "and", arch)
            packagesFilePath := filepath.Join(archDirPath, "Packages")
            packagesxzFilePath := packagesFilePath + ".xz"
            binaryPath := "binary-" + arch

            packagesFile, err := os.Create(packagesFilePath)
            if err != nil {
                fmt.Fprintf(os.Stderr, "Error creating Packages file '%s'\n", packagesFilePath)
                os.Exit(1)
            }

            for _, debToReadPath := range glob(filepath.Join(archDirPath, "*.deb")) {
                scanpackagesOutput := controlFileContents(debToReadPath)
                scanpackagesOutput += "\nFilename: " + filepath.Join("dists", distribution, component, binaryPath, filepath.Base(debToReadPath))
                scanpackagesOutput += "\nSize: " + fmt.Sprint(fileSize(debToReadPath))

                for _, hashType := range hashes {
                    var hashString string
                    if hashType == "md5" {
                        hashString = "MD5Sum"
                    } else {
                        hashString = strings.ToUpper(hashType)
                    }
                    scanpackagesOutput += fmt.Sprintf("\n%s: %x", hashString, hashFile(hashType, debToReadPath))
                }

                fmt.Fprintln(packagesFile, scanpackagesOutput)
                fmt.Fprintln(packagesFile, "")
            }

            packagesFile.Close()
            compressXz(packagesFilePath, packagesxzFilePath)
        }

        for _, contentsFile := range glob(filepath.Join(distributionPath, component, "Contents-*")) {
            compressXz(contentsFile, contentsFile+".xz")
        }
    }

    COMPONENTS = filterDirs(distributionPath)
    fmt.Fprintln(releaseFile, "Components:", strings.Join(COMPONENTS, " "))

    for _, hashType := range hashes {
        var hashString string
        if hashType == "md5" {
            hashString = "MD5Sum"
        } else {
            hashString = strings.ToUpper(hashType)
        }
        fmt.Fprintln(releaseFile, hashString+":")
        for _, component := range COMPONENTS {
            for _, archDirPath := range glob(filepath.Join(distributionPath, component, "binary-*")) {
                for _, f := range []string{"Packages", "Packages.xz"} {
                    filePath := filepath.Join(archDirPath, f)
                    fmt.Fprintf(releaseFile, " %s %d %s\n",
                        hashFile(hashType, filePath),
                        fileSize(filePath),
                        filepath.Join(component, filepath.Base(archDirPath), f))
                }
            }
            for _, contentsFile := range glob(filepath.Join(distributionPath, component, "Contents-*")) {
                fmt.Fprintf(releaseFile, " %s %d %s\n",
                    hashFile(hashType, contentsFile),
                    fileSize(contentsFile),
                    contentsFile)
            }
        }
    }

    if signRepo {
        fmt.Println("Signing with gpg...")
        exec.Command("gpg", "--yes", "--pinentry-mode", "loopback", "--digest-algo", "SHA256", "--clearsign", "-o",
            filepath.Join(distributionPath, "InRelease"), releaseFilePath).Run()
    }

    fmt.Println("Done!")
    fmt.Println()
    fmt.Println("Make the", outputPath, "directory accessible at $REPO_URL")
    fmt.Println()
    fmt.Println("Users can then access the repo by adding a file at")
    fmt.Println("   $PREFIX/etc/apt/sources.list.d")
    fmt.Println("containing:")
    for _, component := range COMPONENTS {
        fmt.Println("   deb [trusted=yes] $REPO_URL", distribution, component)
    }
    fmt.Println()
    fmt.Println("[trusted=yes] is not needed if the repo has been signed with a gpg key")
}

func mapKeys(m map[string]bool) []string {
    var keys []string
    for k := range m {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    return keys
}

func glob(pattern string) []string {
    matches, _ := filepath.Glob(pattern)
    return matches
}

func filterDirs(root string) []string {
    var dirs []string
    files, _ := ioutil.ReadDir(root)
    for _, f := range files {
        if f.IsDir() {
            dirs = append(dirs, f.Name())
        }
    }
    return dirs
}

func hashFile(hashType, filename string) []byte {
    file, err := os.Open(filename)
    if err != nil {
        return nil
    }
    defer file.Close()

    var h hash.Hash
    switch hashType {
    case "md5":
        h = md5.New()
    case "sha1":
        h = sha1.New()
    case "sha256":
        h = sha256.New()
    case "sha512":
        h = sha512.New()
    }
    io.Copy(h, file)
    return h.Sum(nil)
}

func fileSize(filename string) int64 {
    fi, err := os.Stat(filename)
    if err != nil {
        return 0
    }
    return fi.Size()
}

func compressXz(inputPath, outputPath string) {
    inFile, err := os.Open(inputPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error opening input file for xz compression: '%s'\n", inputPath)
        os.Exit(1)
    }
    defer inFile.Close()

    outFile, err := os.Create(outputPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error creating output file for xz compression: '%s'\n", outputPath)
        os.Exit(1)
    }
    defer outFile.Close()

    xzWriter, err := xz.NewWriter(outFile)
    if err != nil {
        fmt.Fprintf(os.Stderr, "Error creating xz writer: %v\n", err)
        os.Exit(1)
    }
    defer xzWriter.Close()

    if _, err := io.Copy(xzWriter, inFile); err != nil {
        fmt.Fprintf(os.Stderr, "Error compressing file to xz: %v\n", err)
        os.Exit(1)
    }
}
