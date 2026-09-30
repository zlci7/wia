World Is Agent
==============

World Is Agent (WIA) is a local AI narrative role-playing application. Choose a
story in your browser, interact with its characters, and continue a saved world.
This package includes the browser client and two built-in stories:
Lantern Dusk and Orbital Repair.

Requirements
------------

Windows x64 and a DeepSeek or OpenAI API key. Go and Node.js are not required
for this packaged executable. This is an internal-playtest build.

Start
-----

    wia-runtime.exe

The application opens your browser and prints a local client URL. Configure a
model in the browser before generating a turn. Keep the process running while
you play; press Ctrl+C in its terminal to stop it.

Options
-------

    wia-runtime.exe -data-root <dir>
    wia-runtime.exe -http-addr 127.0.0.1:8765
    wia-runtime.exe -model-config <file>
    wia-runtime.exe -story-packs <dir>
    wia-runtime.exe -no-open

With -no-open, open the printed local client URL, including its session token.
The HTTP address must be loopback.

Saves and configuration
-----------------------

The default data root is %LOCALAPPDATA%\WorldIsAgent. Set WIA_DATA_ROOT or use
-data-root to choose another location. Keep this directory when updating the
executable; it contains your saves and model configuration.

Under the data root, story-app/app.db stores the application catalog. Each world
has its own database under story-app/worlds/. Model configuration and credentials
are in story-app/config/ and story-app/secrets/. Existing worlds retain their
story definitions when the story catalog changes.

Use the in-app save-as operation to create an independent branch. Only one
application process may use a data root at a time.

License
-------

MIT. See LICENSE.
