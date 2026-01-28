#!/usr/bin/env stack
{- stack script
   --resolver lts-22.28
   --package lsp-test
   --package hspec
   --package text
   --package lsp-types
   --package data-default
   --package lens
-}

{-# LANGUAGE OverloadedStrings #-}

module Main where

import Test.Hspec
import Language.LSP.Test
import Language.LSP.Protocol.Types
import qualified Data.Text as T
import Control.Monad.IO.Class (liftIO)
import Data.Default (def)

-- Minimal capabilities for testing
minimalCaps :: ClientCapabilities
minimalCaps = fullCaps

serverPath :: String
serverPath = "/home/justin/src/melange-lsp/melange-lsp"

main :: IO ()
main = hspec $ do
  describe "melange-lsp" $ do
    it "initializes successfully" $ do
      runSessionWithConfig (def { messageTimeout = 5 }) serverPath minimalCaps "." $ do
        doc <- createDoc "test.yaml" "yaml" ""

        -- Just check that initialization worked
        return ()

    it "provides document symbols" $ do
      let testDoc = T.unlines
            [ "package:"
            , "  name: test-package"
            , "  version: 1.0.0"
            , "  epoch: 0"
            , ""
            , "pipeline:"
            , "  - uses: fetch"
            , "    with:"
            , "      uri: https://example.com/file.tar.gz"
            , "  - name: build step"
            , "    runs: make install"
            ]

      runSessionWithConfig (def { messageTimeout = 5 }) serverPath minimalCaps "." $ do
        doc <- createDoc "test.yaml" "yaml" testDoc
        symbols <- getDocumentSymbols doc

        -- Check we got some symbols back
        liftIO $ symbols `shouldSatisfy` (not . null)

    it "reports diagnostics for invalid YAML" $ do
      let invalidDoc = T.unlines
            [ "package:"
            , "  name: test-package"
            , "  -- missing version and epoch"
            ]

      runSessionWithConfig (def { messageTimeout = 5 }) serverPath minimalCaps "." $ do
        _ <- createDoc "invalid.yaml" "yaml" invalidDoc
        diags <- waitForDiagnostics

        -- Should get at least one diagnostic
        liftIO $ diags `shouldSatisfy` (not . null)

    it "provides completions for package fields" $ do
      let testDoc = T.unlines
            [ "package:"
            , "  "
            ]

      runSessionWithConfig (def { messageTimeout = 5 }) serverPath minimalCaps "." $ do
        doc <- createDoc "completion.yaml" "yaml" testDoc
        -- Position at line 1 (0-indexed), character 2 (after the indentation)
        completions <- getCompletions doc (Position 1 2)

        -- Should get package field completions (name, version, epoch, etc.)
        -- Package has many fields, we should get several completions
        liftIO $ length completions `shouldSatisfy` (> 5)
