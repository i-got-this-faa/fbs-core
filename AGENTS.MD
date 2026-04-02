## general guidelines:
- when installing new packages, use cli instead of manually editing the pkg file and dont include the pkg version, let it be determined by the cli
- avoid as any at all costs, try to infer types from functions as much as possible
- dont write monolithic files, break them down into smaller, reusable pieces
- make sure to write tests for new functionality, and update existing tests if you change behavior
- follow the go code style guide for formatting and naming conventions
- use descriptive names for variables, functions, and components to improve readability
- keep functions small and focused on a single task
- avoid side effects in functions, try to make them pure whenever possible


## Before You Start Coding
### Ask Yourself:

1. **Does this already exist?**
   - Search the codebase for similar functionality
   - Check the utility folders listed in "Core Principles"

2. **Can I extend something existing?**
   - Maybe a utility just needs one more function
   - Maybe a component just needs one more prop

3. **Where should this live?**
   - Is it reusable? -> Put it in utils or components
   - Is it specific to one feature? → Keep it local
   - Is it a constant? → Put in config

4. **Am I duplicating anything?**
   - If you're copying code, stop and extract it
   - If you're defining the same type twice, use the existing one

5. **Is this function doing too much?**
   - Can you describe it in one sentence without "and"?
   - If not, break it down

## If you incounter a problem that you failed to solve and took multile back and forth with me, Document it here 
