// Package src contains the de-facto main function of the application.
// It should set everything up, create a library and create a webserver.
//
// At the moment it is in package src because I import it from the project's root
// folder. This way the source is in the `src/` directory.
package src

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/ironsmile/httpms/ca"
	"github.com/ironsmile/httpms/src/config"
	"github.com/ironsmile/httpms/src/daemon"
	"github.com/ironsmile/httpms/src/helpers"
	"github.com/ironsmile/httpms/src/library"
	"github.com/ironsmile/httpms/src/webserver"
)

var (
	// pidFile is populated by an command line argument. Will be a filesystem path.
	// Nedomi will save its Process ID in this file.
	pidFile string

	// debug is populated by an command line argument.
	debug bool

	// showVersion would be true when the -v flag is used
	showVersion bool

	// rescanLibrary is populated by the -rescan flag and will cause a single
	// scan to move through all the items in the database and update their
	// meta data with whatever is present in the source.
	rescanLibrary bool
)

const userAgentFormat = "HTTP Media Server/%s (github.com/ironsmile/httpms)"

func init() {
	flag.StringVar(&pidFile, "p", "pidfile.pid",
		"Lock file which will be used for making sure only one\n"+
			"instance of the server is currently runnig. The default\n"+
			"location is is [user_path]/pidfile.pid.")
	flag.BoolVar(&debug, "D", false, "Debug mode. Will log everything to the stdout.")
	flag.BoolVar(&showVersion, "v", false, "Show version and build information.")
	flag.BoolVar(&rescanLibrary, "rescan", false,
		"Will metadata synchronization with the source. All media in\n"+
			"the database will be updated. Without starting the server proper.")
}

// Main is the only thing run in the project's root main.go file.
// For all intent and purposes this is the main function.
func Main(httpRootFS, htmlTemplatesFS, sqlFilesFS fs.FS) {
	flag.Parse()

	if showVersion {
		printVersionInformation()
		os.Exit(0)
	}

	if rescanLibrary {
		log.Println("TODO: implement rescan")
		os.Exit(0)
	}

	if err := runServer(
		httpRootFS,
		htmlTemplatesFS,
		sqlFilesFS,
	); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

// setupPidFileAndSignals creates a pidfile and starts a signal receiver goroutine.
func setupPidFileAndSignals(pidFileName string, stopFunc context.CancelFunc) {
	helpers.SetUpPidFile(pidFileName)

	signalChannel := make(chan os.Signal, 2)
	for _, sig := range daemon.StopSignals {
		signal.Notify(signalChannel, sig)
	}
	go func() {
		for range signalChannel {
			log.Println("Stop signal received. Removing pidfile and stopping.")
			stopFunc()
			helpers.RemovePidFile(pidFileName)
		}
	}()
}

// Returns a new Library object using the application config.
// For the moment this is a LocalLibrary which will place its sqlite db file
// in the UserPath directory
func getLibrary(
	ctx context.Context,
	userPath string,
	cfg config.Config,
	sqlFilesFS fs.FS,
) (*library.LocalLibrary, error) {

	dbPath := helpers.AbsolutePath(cfg.SqliteDatabase, userPath)
	lib, err := library.NewLocalLibrary(ctx, dbPath, sqlFilesFS)

	if err != nil {
		return nil, err
	}

	lib.ScanConfig = cfg.LibraryScan

	err = lib.Initialize()

	if err != nil {
		return nil, err
	}

	for _, path := range cfg.Libraries {
		lib.AddLibraryPath(path)
	}

	if cfg.DownloadArtwork {
		useragent := fmt.Sprintf(userAgentFormat, Version)
		caf := ca.NewClient(useragent, time.Second)
		lib.SetCoverArtFinder(caf)
	}

	return lib, nil
}

// runServer parses the config, sets the logfile, setups the
// pidfile, and makes an signal handler goroutine
func runServer(httpRootFS, htmlTemplatesFS, sqlFilesFS fs.FS) error {
	cfg, err := config.FindAndParse()
	if err != nil {
		return fmt.Errorf("parsing configuration: %s", err)
	}

	userPath := filepath.Dir(config.UserConfigPath())

	if !debug {
		err = helpers.SetLogsFile(helpers.AbsolutePath(cfg.LogFile, userPath))
		if err != nil {
			return fmt.Errorf("setting debug file: %s", err)
		}
	}

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	pidFileName := helpers.AbsolutePath(pidFile, userPath)
	setupPidFileAndSignals(pidFileName, cancelCtx)
	defer helpers.RemovePidFile(pidFileName)

	lib, err := getLibrary(ctx, userPath, cfg, sqlFilesFS)
	if err != nil {
		return err
	}

	if !cfg.LibraryScan.Disable {
		go lib.Scan()
	}

	log.Printf("Release %s\n", Version)
	srv := webserver.NewServer(ctx, cfg, lib, httpRootFS, htmlTemplatesFS)
	srv.Serve()
	srv.Wait()
	return nil
}
