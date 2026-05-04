#!/usr/bin/env fish
# cleanup-and-build.fish — Clean up temp-tracker binary, kill running instances, and rebuild
#
# Like a Python script that:
#   - os.remove('temp-tracker') to delete the binary
#   - subprocess.run(['pgrep', '-f', 'temp-tracker']) to find PIDs
#   - os.kill(pid, signal.SIGTERM) to stop processes
#   - subprocess.run(['go', 'build', ...]) to recompile
#
# In Fish (unlike Bash), variables use $var without braces, and command substitution is (cmd)
# not $(cmd) or `cmd`.

# Colors for output (like Python's colorama or rich library)
set -l GREEN '\033[0;32m'
set -l YELLOW '\033[1;33m'
set -l RED '\033[0;31m'
set -l NC '\033[0m'  # No Color — reset to default

echo -e "${YELLOW}🧹 Cleaning up temp-tracker...${NC}"

# Step 1: Remove the binary if it exists
# test -f is like Python's os.path.isfile() or pathlib.Path.exists()
if test -f ./temp-tracker
    rm ./temp-tracker
    echo -e "${GREEN}✓ Binary removed${NC}"
else
    echo -e "${YELLOW}⚠ No binary found to remove${NC}"
end

# Step 2: Find and kill running temp-tracker processes
# pgrep -f matches the full command line — like Python's psutil.process_iter() checking cmdline()
# The -f flag is crucial: without it, pgrep only matches the process name, not the full command with args
set -l pids (pgrep -f "temp-tracker")

# Check if we found any PIDs (non-empty string)
# In Fish: if test -n "$pids" is like Python's if pids: (check if string not empty)
if test -n "$pids"
    echo -e "${YELLOW}🔍 Found running temp-tracker process(es): $pids${NC}"
    
    # Iterate over each PID — like Python's for pid in pids.split():
    for pid in $pids
        echo -e "${YELLOW}  → Killing PID $pid${NC}"
        # kill is like Python's os.kill(pid, signal.SIGTERM)
        # The -15 flag means SIGTERM (graceful shutdown), not SIGKILL (-9)
        kill -15 $pid 2>/dev/null
        
        # Wait a moment for graceful shutdown — like Python's time.sleep(0.5)
        # Fish's sleep accepts decimals (unlike some shells)
        sleep 0.5
        
        # Check if process still exists — kill -0 tests existence without sending a real signal
        # In Python: try os.kill(pid, 0) except ProcessLookupError: ...
        if kill -0 $pid 2>/dev/null
            echo -e "${RED}  ⚠ PID $pid still running, forcing kill...${NC}"
            kill -9 $pid 2>/dev/null  # SIGKILL — force terminate, like Python's process.terminate()
        else
            echo -e "${GREEN}  ✓ PID $pid stopped${NC}"
        end
    end
else
    echo -e "${GREEN}✓ No running temp-tracker processes found${NC}"
end

# Step 3: Rebuild the binary
echo -e "${YELLOW}🔨 Building temp-tracker...${NC}"

# Run go build — like Python's subprocess.run(['go', 'build', '-o', 'temp-tracker', '.'])
# Check exit status with $status — like Python's subprocess.run().returncode
# In Fish, the status variable is set automatically after each command
go build -o temp-tracker .

if test $status -eq 0
    echo -e "${GREEN}✓ Build successful!${NC}"
    echo ""
    echo -e "${GREEN}🚀 Ready to run with:${NC}"
    echo -e "   ${YELLOW}nohup ./temp-tracker -port 9091 -interval 30 > tracker.log 2>&1 &${NC}"
else
    echo -e "${RED}✗ Build failed!${NC}"
    exit 1  # Like Python's sys.exit(1) — return error code to shell
end

echo ""
echo -e "${GREEN}✨ Cleanup and rebuild complete!${NC}"