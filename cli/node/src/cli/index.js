// Wires the command table and builds an App bound to the process.
import { App } from './app.js';
import { SystemKeychain } from './auth.js';
import { execGit } from './git.js';
import { registerContextCommands } from './commands-context.js';
import { registerStatusCommands } from './commands-status.js';
import { registerToolCommands } from './tools.js';

let registered = false;
export function registerAll() {
  if (registered) return;
  registerContextCommands();
  registerStatusCommands();
  registerToolCommands();
  registered = true;
}

export function newApp() {
  registerAll();
  return new App({
    stdin: process.stdin, stdout: process.stdout, stderr: process.stderr,
    env: process.env, cwd: process.cwd(), keychain: new SystemKeychain(),
    git: execGit, now: () => new Date(),
    stdoutIsTTY: !!process.stdout.isTTY, stdinIsTTY: !!process.stdin.isTTY,
  });
}

export { App };
