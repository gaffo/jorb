JORB
================================

Jorb's jorbs is to make your asynchronous job processing life easier.


Basic Usage
-------------------------------

Create a context
<pre>
import org.gaffo.jorb.pico.PicoContainerContext;
import org.gaffo.jorb.hornetq.

// ...

IContext context = new PicoContainerContext();
</pre>

Next add implemenations to the context for any of the classes that your jorbs are going to need to do their work.

<pre>
IUserProvider userProvider = new UserProvider(); // example class, use your own
context.register(userProvider);
</pre>


