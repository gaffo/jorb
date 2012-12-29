package org.gaffo.jorb.hornetq;

import org.gaffo.jorb.IContext;
import org.gaffo.jorb.IJob;
import org.gaffo.jorb.IQueue;
import org.gaffo.jorb.JSONSerializer;
import org.hornetq.api.core.HornetQException;
import org.hornetq.api.core.TransportConfiguration;
import org.hornetq.api.core.client.*;
import org.hornetq.core.config.Configuration;
import org.hornetq.core.config.impl.ConfigurationImpl;
import org.hornetq.core.remoting.impl.invm.InVMAcceptorFactory;
import org.hornetq.core.remoting.impl.invm.InVMConnectorFactory;
import org.hornetq.core.server.HornetQServer;
import org.hornetq.core.server.HornetQServers;

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public class HornetQ implements IQueue
{

    private final HornetQServer server;
    private final ClientSessionFactory sf;
    private String name;
    private final ClientProducer producer;
    private final ClientSession session;
    private final ExecutorService pool;
    private final IContext context;
    private final JSONSerializer serializer;

    public HornetQ(String name, int cThreads, IContext context) throws Exception
    {
        this.serializer = new JSONSerializer();
        this.name = name;
        this.context = context;

        // Step 1. Create the Configuration, and set the properties accordingly
        Configuration configuration = new ConfigurationImpl();
        //we only need this for the server lock file
        configuration.setJournalDirectory("target/data/journal");
        configuration.setPersistenceEnabled(false);
        configuration.setSecurityEnabled(false);
        configuration.getAcceptorConfigurations().add(new TransportConfiguration(InVMAcceptorFactory.class.getName()));

        // Step 2. Create and start the server
        server = HornetQServers.newHornetQServer(configuration);
        server.start();

        // Step 3. As we are not using a JNDI environment we instantiate the objects directly
        ServerLocator serverLocator = HornetQClient.createServerLocatorWithoutHA(new TransportConfiguration(InVMConnectorFactory.class.getName()));
        sf = serverLocator.createSessionFactory();

        // Step 4. Create a core queue
        ClientSession coreSession = sf.createSession(false, false, false);

        coreSession.createQueue(name, name, true);

        coreSession.close();

        // Step 5. Create the session, and producer
        session = sf.createSession();

        producer = session.createProducer(name);


        // Step 7. Create the message consumer and start the connection
        ClientConsumer messageConsumer = session.createConsumer(name);
        session.start();

        messageConsumer.setMessageHandler((ClientMessage message)->{OnMessage(message);});

        pool = Executors.newFixedThreadPool(cThreads);
    }

    protected void OnMessage(ClientMessage message)
    {
        String className = message.getStringProperty("subject");
        System.out.printf("processing job of class %s\n", className);

        String body = message.getStringProperty("body");
        System.out.println("Processing: " + body);
        final IJob job = serializer.fromJson(body);
        pool.execute(()->{job.process(context);});
    }

    public void shutdown() throws Exception
    {
        // Step 9. Be sure to close our resources!
        if (sf != null)
        {
            sf.close();
        }

        // Step 10. Stop the server
        server.stop();
        pool.shutdownNow();
    }

    @Override
    public void enqueue(IJob job) throws EnqueueException
    {
        // Step 6. Create and send a message
        ClientMessage hqMsg = session.createMessage(false);

        hqMsg.putStringProperty("subject", job.getClass().getName());
        String data = serializer.toJson(job);
        hqMsg.putStringProperty("body", data);

        try
        {
            producer.send(hqMsg);
        } catch (HornetQException e)
        {
            throw new EnqueueException(e);
        }
    }

    public class EnqueueException extends Exception
    {
        public EnqueueException(Exception e)
        {
            super(e);
        }
    }
}
