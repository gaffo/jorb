JORB
================================

Jorb's jorbs is to make your asynchronous job processing life easier.


Basic Usage
-------------------------------

Create a context
<pre>
import org.gaffo.jorb.pico.PicoContainerContext;
import org.gaffo.jorb.hornetq.HornetQ;

// ...

IContext context = new PicoContainerContext();
IQueue queue = new HornetQ("queueName", 3 /* threads */, context);
</pre>

Next add implementations to the context for any of the classes that your jorbs are going to need to do their work.

<pre>
IUserProvider userProvider = new UserProvider(); // example class, use your own
context.register(userProvider);
</pre>

Make a job class. 
- Make any fields you need to do your job as fields, so that they can be serialized. 
- Also make a process method. It can take any # of parameters except just a context. All of the parameters will be
populated by the context you created above.
<pre>
class MyJob extends ACH
{
    public String username;
    
    public MyJob(String username)
    {
        this.username = username;
    }

    public void process(UserProvider userProvider)
    {
        // Example work
        User user = userProvider.findUserByUsername(username);
        user.notify();
    }
}
</pre>

Now queue up your work and it'll be executed:
<pre>
queue.enqueue(new Job("gaffo"));
</pre>
