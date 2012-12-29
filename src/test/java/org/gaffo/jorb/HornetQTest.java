package org.gaffo.jorb;

import org.gaffo.jorb.hornetq.HornetQ;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;
import org.mockito.Mock;
import org.mockito.MockitoAnnotations;

import java.util.concurrent.atomic.AtomicBoolean;

import static org.junit.Assert.assertTrue;
import static org.mockito.Mockito.when;

public class HornetQTest
{
    private HornetQ q;
    @Mock IContext mockContext;

    @Before
    public void setup() throws Exception
    {
        MockitoAnnotations.initMocks(this);
        q = new HornetQ("hqt", 1, mockContext);
    }

    @After
    public void tearDown() throws Exception
    {
        q.shutdown();
    }

    @Test
    public void processOne() throws InterruptedException, HornetQ.EnqueueException
    {
        AtomicBoolean ab = new AtomicBoolean(false);
        when(mockContext.get(AtomicBoolean.class)).thenReturn(ab);
        q.enqueue(new TestJob());

        Thread.sleep(100);

        assertTrue(ab.get());
    }
}
