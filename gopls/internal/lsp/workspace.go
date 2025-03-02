// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsp

import (
	"context"
	"fmt"

	"golang.org/x/tools/gopls/internal/lsp/protocol"
	"golang.org/x/tools/gopls/internal/lsp/source"
	"golang.org/x/tools/gopls/internal/span"
	"golang.org/x/tools/internal/event"
)

func (s *Server) didChangeWorkspaceFolders(ctx context.Context, params *protocol.DidChangeWorkspaceFoldersParams) error {
	event := params.Event
	for _, folder := range event.Removed {
		view := s.session.View(folder.Name)
		if view != nil {
			s.session.RemoveView(view)
		} else {
			return fmt.Errorf("view %s for %v not found", folder.Name, folder.URI)
		}
	}
	return s.addFolders(ctx, event.Added)
}

// addView returns a Snapshot and a release function that must be
// called when it is no longer needed.
func (s *Server) addView(ctx context.Context, name string, uri span.URI) (source.Snapshot, func(), error) {
	s.stateMu.Lock()
	state := s.state
	s.stateMu.Unlock()
	if state < serverInitialized {
		return nil, nil, fmt.Errorf("addView called before server initialized")
	}
	options := s.session.Options().Clone()
	if err := s.fetchConfig(ctx, name, uri, options); err != nil {
		return nil, nil, err
	}
	_, snapshot, release, err := s.session.NewView(ctx, name, uri, options)
	return snapshot, release, err
}

func (s *Server) didChangeConfiguration(ctx context.Context, _ *protocol.DidChangeConfigurationParams) error {
	ctx, done := event.Start(ctx, "lsp.Server.didChangeConfiguration")
	defer done()

	// Apply any changes to the session-level settings.
	options := s.session.Options().Clone()
	if err := s.fetchConfig(ctx, "", "", options); err != nil {
		return err
	}
	s.session.SetOptions(options)

	// Go through each view, getting and updating its configuration.
	for _, view := range s.session.Views() {
		options := s.session.Options().Clone()
		if err := s.fetchConfig(ctx, view.Name(), view.Folder(), options); err != nil {
			return err
		}
		view, err := s.session.SetViewOptions(ctx, view, options)
		if err != nil {
			return err
		}
		go func() {
			snapshot, release, err := view.Snapshot()
			if err != nil {
				return // view is shut down; no need to diagnose
			}
			defer release()
			s.diagnoseDetached(snapshot)
		}()
	}

	// An options change may have affected the detected Go version.
	s.checkViewGoVersions()

	return nil
}
