#!/usr/bin/env stack
{- stack script
   --resolver lts-22.28
   --package lsp-test
   --package hspec
   --package text
   --package lsp-types
-}

{-# LANGUAGE OverloadedStrings #-}

module Main where

import Test.Hspec
import Language.LSP.Test
import Language.LSP.Protocol.Types
import qualified Data.Text as T

main :: IO ()
main = hspec $ do
  describe "melange-lsp" $ do
    it "initializes successfully" $ do
      runSession "../../melange-lsp" fullCaps "." $ do
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

      runSession "../../melange-lsp" fullCaps "." $ do
        doc <- createDoc "test.yaml" "yaml" testDoc
        symbols <- getDocumentSymbols doc

        -- Check we got some symbols back
        liftIO $ symbols `shouldSatisfy` (not . null)
