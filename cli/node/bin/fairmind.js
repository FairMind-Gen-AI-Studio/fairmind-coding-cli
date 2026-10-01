#!/usr/bin/env node
// Entry point of the `fairmind` CLI (Node edition). package.json `bin` points here.
import { newApp } from '../src/cli/index.js';

process.exitCode = await newApp().run(process.argv.slice(2));
