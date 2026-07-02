#!/bin/sh

echo "🧵 Styling, testing and building the code before committing..."

# 1. Check gofmt standards across the entire monorepo
if [ "$(gofmt -l . | wc -l)" -ne 0 ]; then
    echo "❌ gofmt check failed. Run: gofmt -w ."
    echo "Are you seriously trying to skip this? Really? Run 'gofmt -w .' to format your code and try again.\n"
    exit 1
fi

# 2. Check golangcli-lint standards using the modern 'run' command
if command -v golangcli-lint > /dev/null 2>&1; then
    # In a Go workspace, running 'golangcli-lint run' automatically evaluates go.work modules
    golangcli-lint run ./...
    if [ $? -ne 0 ]; then
        echo "\n❌😤😤 Get this weak stuff outta here😤👋. golangcli-lint check failed. Fix lint errors and try again."
        exit 1
    fi
fi

# 3. Run tests across all workspace modules natively
echo "🔍 Running tests..."
OIFS="$IFS"
IFS='
'
for dir in $(go list -m -f '{{.Dir}}'); do
    (cd "$dir" && go test ./...)
    if [ $? -ne 0 ]; then
        echo "\n❌ Better call Bob...coz your build failed. ❌😤🪓"
        echo "Go tests failed in $dir: View the logs above.\n"
        IFS="$OIFS"
        exit 1
    fi
done
IFS="$OIFS"

echo " 😎😎😎...✅ lookin' good nerd. Don't be happy yet, trying to build apps now...😎😎😆😆"

# 4. Verify compilation for our gateway application (and future services)
# Since go build at the workspace root has no files, we explicitly verify our app targets compile
go build -o /dev/null ./apps/api-gateway/cmd/main.go
if [ $? -ne 0 ]; then
 echo "\n❌❌🪓 Better call Bob...coz your build failed. ❌😤🪓. Go build failed: View the errors above"
 exit 1 
fi

echo "✅ All checks passed! Committing your clean code."
