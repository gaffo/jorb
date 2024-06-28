# JORB - Concentrated Computing

Jorb is a Concentrated (as opposed to Distributed) workflow system. Single computers are BIG and FAST these days. 
You can do a lot with them, but there aren't many nice workflow composition systems that don't require all sorts of distributed craziness 
like a message queue and multiple nodes and processes.

Jorb is that system for golang.

I use it when I have complex batch jobs that need:
- To not run in a massively complex distributed computing system
- Easy to debug and expand while in flight (who doesn't code in the debugger)
- Rate limiting per step
- Concurrency controls per step
- Fancy Status bars (I found that https://github.com/vbauerster/mpb makes a good status listener, not provided currently)
- Error recovery
- State checkpointing
- Very easy to stop and restart at whim

For instance I'm currently using it to power large sets of code changes, where I need to check out packages, make changes, commit, trial build on a fleet, monitor status,
fork state depending on build success or failure, cut cr, send cr via email/slack/etc, check status. Many of these steps have rate limits with services or sane rate limits with the
OS.

# Quick Start

The unit tests in processor_test.go provide a pretty good example to get started, for instance https://github.com/gaffo/jorb/blob/main/processor_test.go#L111

# Concepts

When making a program you basically need the following: AC, OC, JC, Job, Run, States, StatusListener, Serializer, and a Processor

## AC: Application Context
This is a struct where you can pass job specific application context to the jobs as they execute state transitions.
Generaly I put implementations or funcs that let me call apis, like aws sdk clients.

This is usually epmemeral per go process run, don't keep state here, just glue your app into this

This is passed into each state exec function invocation

## OC: Overalll Context
This is the overall state for your job, store state (generally immutable) here for values that all of your jobs in a Run will need. 
I'll store things like a database name, or file name or work directory that is specific to that run.

This is passed into each state exec function invocation

## JC: Job Context
This is a per-job state that is specific to this workflow. Go wide table format here, using a union of all of the fields that all of this workflow's states
will need.

This is passed into each state exec function invocation. You mutate it locally and return the updated JC.

*NOTE*: These fields need to be JSON serialzable.

## Job
A job has a State (string) and a JC which contains your workflow specific state for each job. Jobs also track their state transitions, parent jobs, and errors per state.

## Run
A run is a serializable group of jobs. Generally you create a run and add jobs to it then fire it at a processor. Or you load a previous job with a serialzier, fire it
at a processor. It's meant to be super restartable.

You can also load up a run and spit outreports once it's been fully processed (or reallly at any time). It contains ALL of the state for a job other than the AC.

## States
A State is a description of a possible state that a job can be in, a state has:

* A TriggerState which is a string matching the state of the jobs you want this state to process
* An optional ExecFunction which does the acutal processing (more in a sec)
* Terminal: if the state is terminal, then it won't process, and a run will be considered complete when all jobs are in terminal states. Fun note, you can just swap in code on if a state
is terminal to patch up workflows or to stop certain actions (I turn terminal off in off hours so I don't send actual CRs, just all the pre-validation). flag.Bool works great for this.
* Concurrency: the number of concurrent procesors for this state, this is nice if the steps take a while esp on network calls
* RateLimit: a rate.Limit that is shared by all processors for this state, great if you are hitting a rate limited api

Typically you want to be pretty granular with your steps. For instance in a recent workflow I have seperate states for:
* File modification
* Git Add
* Git Commit
* Git Push
* Git Fetch
* Git rev-parse
This really just helps with debugging and "redriving" where you need to quickly send all of a state back to another state to patch them up, esp when developing flows and
handling errors.

### ExecFunc
This is the meat of the work. The general contract is that it is called with:
* AC - execution (app) context
* OC - overall context
* JC - app specific context

And returns: 
* JC - updated with the state mutations that state did
* NewState - the next state you want to go to with this job. It's totaly fine to go to the same state or previous states. On errors I usually come back to the same state which is basically a retry, or go to a seperate terminal state for specific errors (this is good because it allows you to easily redrive by editing the state file with find and replace).
* optional []KickRequests - these are requests to span new jobs. This is how you fork a workflow. For instance if you get data from a table and you want to fire a lot of S3 fetches, use this.
it's fine if you take the job that kicked everythign else and send it to a termainal state and do all the other work, or just re-use it as the first of many. Kicks will get a job ID that is ${parent_id}->${new_seq}.
* error - This is logged on the job by state and will eventually have logic for retries and termination if there are too many

# StatusListener
You can use a nil one but I hook this up to a hash of progress bars per state to show my status.

You have to have one cause I'm too lazy to deal with nil.

# Serializer
I reallly recommend you use one, there's a JsonSerializer provided, just new it up. This lets you very easily kill and restart processing of the workflow 
constantly or at any time. It also lets you re-hydrate old workflows and report on them.

If you really don't want to use one then there's a NilSerializer you can use. 

# Processor
This does all the work, new one up with a app context and set of states and then exec a run with it. It'll block until it finishes calling to the ExecFunctions, Serializer, and 
StatusListener as needed.

# Other Notes
This is super alpha software. I am point releasing it every breaking change at the v0.0.x level. 

There are several places where I just log.Fatal if the app is in an invalid state especially if a unknown state string is found. The nice thing
is the app tries to checkpoint the run file state every job completion, so you usually lose little information.

State saving: about that, work is definitely queueing up before it gets serialized. I need to optimize this better with batching so I'm not doing as many expensive saves.

Throughput: speaking of throughput, there's definitely room for improvement here.  Too much work is getting queued up for the processors. 
Too much work is stacking up on the return queue.

Metrics: I'd really like to get metrics around how long states are taking, how many are executing on average, and where the bottle necks are. I think that many of the
states can be dynamically adjusted on concurrency for optimal performance (when you do memory or disk or cpu heavy jobs). 

No adaptive rate limiting or rate limit error handling. Many apis tell you when you're hitting rate limits, 
need a way to message that back to the stae procssor so you don't have to manually dial in rate limits and can just let the system adapt.

It uses slog, but doesn't setup a default logger, you can fix this by creating a file logger. It's VERY spammy if you don't. 

Testing and refactoring is needed. It's getting better but testing a system like this is complex, and I need to pull some of the major functions into their own functions so I can test a lot of the edge cases
without firing up a big job.
